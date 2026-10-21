package pool

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"icloud-hme/internal/hme"
)

// fakeBackend 是内存版上游,记录创建调用便于断言。
type fakeBackend struct {
	mu         sync.Mutex
	ids        []string
	aliases    map[string][]hme.Alias
	created    []string          // 每次创建传入的 label(自动补号应恒为空)
	assigned   map[string]string // email -> groupID
	failCreate int               // 剩余允许失败的创建次数
	alwaysFail bool
	failList   bool
	seq        int
}

func newFakeBackend(ids ...string) *fakeBackend {
	return &fakeBackend{ids: ids, aliases: map[string][]hme.Alias{}, assigned: map[string]string{}}
}

func (f *fakeBackend) AccountIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ids...)
}

func (f *fakeBackend) ListAliases(accountID string) ([]hme.Alias, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList {
		return nil, fmt.Errorf("上游不可用")
	}
	return append([]hme.Alias(nil), f.aliases[accountID]...), nil
}

func (f *fakeBackend) CreateAlias(accountID, label string) (*hme.CreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.alwaysFail || f.failCreate > 0 {
		if f.failCreate > 0 {
			f.failCreate--
		}
		return nil, fmt.Errorf("创建失败")
	}
	f.seq++
	f.created = append(f.created, label)
	email := fmt.Sprintf("fake%d@icloud.com", f.seq)
	f.aliases[accountID] = append(f.aliases[accountID], hme.Alias{
		Email:       email,
		AnonymousID: fmt.Sprintf("a%d", f.seq),
		Label:       label,
		Active:      true,
		CreatedAt:   time.Now().Format(time.RFC3339),
	})
	return &hme.CreateResult{Email: email, Label: label, CreatedAt: time.Now().Format(time.RFC3339)}, nil
}

func (f *fakeBackend) AssignGroup(accountID, email, groupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if groupID == "" {
		delete(f.assigned, email)
		return nil
	}
	f.assigned[email] = groupID
	return nil
}

// seed 预置别名,active 控制启用状态。
func (f *fakeBackend) seed(accountID string, active, inactive int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < active; i++ {
		f.seq++
		f.aliases[accountID] = append(f.aliases[accountID], hme.Alias{
			Email: fmt.Sprintf("active%d@icloud.com", f.seq), AnonymousID: fmt.Sprintf("s%d", f.seq), Active: true,
		})
	}
	for i := 0; i < inactive; i++ {
		f.seq++
		f.aliases[accountID] = append(f.aliases[accountID], hme.Alias{
			Email: fmt.Sprintf("off%d@icloud.com", f.seq), AnonymousID: fmt.Sprintf("s%d", f.seq), Active: false,
		})
	}
}

func (f *fakeBackend) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

func newTestPool(t *testing.T, be *fakeBackend) *Pool {
	t.Helper()
	p, err := New(t.TempDir(), be)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.createGap = 0 // 测试里不需要真实间隔
	t.Cleanup(p.Close)
	return p
}

// TestCheckOnceFillsGap 验证低于目标时按差额创建。
func TestCheckOnceFillsGap(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.seed("acc_1", 2, 0)
	p := newTestPool(t, be)

	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 5, MaxPerRun: 5}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	res, err := p.CheckOnce("acc_1")
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if res.Current != 2 {
		t.Fatalf("当前数量应为 2,实际 %d", res.Current)
	}
	if res.Created != 3 {
		t.Fatalf("应创建 3 个,实际 %d", res.Created)
	}
	if len(res.Emails) != 3 {
		t.Fatalf("应返回 3 个新邮箱,实际 %v", res.Emails)
	}
	if n := be.createdCount(); n != 3 {
		t.Fatalf("上游应收到 3 次创建,实际 %d", n)
	}

	// 再跑一次不应再创建
	again, _ := p.CheckOnce("acc_1")
	if again.Created != 0 {
		t.Fatalf("已达标不应再创建,实际 %d", again.Created)
	}
	if n := be.createdCount(); n != 3 {
		t.Fatalf("总数应保持 3,实际 %d", n)
	}
}

// TestCheckOnceRespectsMaxPerRun 验证单轮上限,防止一次补爆。
func TestCheckOnceRespectsMaxPerRun(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)

	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 20, MaxPerRun: 2}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, _ := p.CheckOnce("acc_1")
	if res.Created != 2 {
		t.Fatalf("单轮上限 2,实际创建 %d", res.Created)
	}
	if res.Current != 0 {
		t.Fatalf("初始当前数量应为 0,实际 %d", res.Current)
	}
}

// TestCheckOnceCountMode 验证 active/all 两种口径。
func TestCheckOnceCountMode(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.seed("acc_1", 2, 3) // 2 启用 + 3 停用 = 5 条
	p := newTestPool(t, be)

	// 只算启用:2 < 3,应补 1
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 3, CountMode: "active"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, _ := p.CheckOnce("acc_1")
	if res.Current != 2 || res.Created != 1 {
		t.Fatalf("active 口径应 2 → 补 1,实际 current=%d created=%d", res.Current, res.Created)
	}

	// 算全部:5 >= 3,不再补
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 3, CountMode: "all"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	be.mu.Lock()
	be.created = nil
	be.mu.Unlock()
	res, _ = p.CheckOnce("acc_1")
	if res.Current != 6 { // 5 + 上一步补的 1
		t.Fatalf("all 口径当前应为 6,实际 %d", res.Current)
	}
	if res.Created != 0 {
		t.Fatalf("all 口径已达标不应创建,实际 %d", res.Created)
	}
}

// TestCheckOncePausesAfterConsecutiveFailures 验证连续失败自动暂停。
func TestCheckOncePausesAfterConsecutiveFailures(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.alwaysFail = true
	p := newTestPool(t, be)

	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 5}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	for i := 0; i < maxConsecutiveFail; i++ {
		if _, err := p.CheckOnce("acc_1"); err != nil {
			t.Fatalf("第 %d 次 CheckOnce: %v", i+1, err)
		}
	}
	cfg, _ := p.Get("acc_1")
	if !cfg.Paused {
		t.Fatalf("连续失败 %d 次应自动暂停,实际 %+v", maxConsecutiveFail, cfg)
	}
	if !strings.Contains(cfg.LastError, "暂停") {
		t.Fatalf("LastError 应说明已暂停,实际 %q", cfg.LastError)
	}

	// 暂停后 runDue 不应再尝试
	p.runDue()
	if cfg2, _ := p.Get("acc_1"); cfg2.ConsecutiveFailures != cfg.ConsecutiveFailures {
		t.Fatal("暂停的账号不应被调度")
	}
}

// TestUpsertClearsPaused 验证用户重新保存会解除暂停。
func TestUpsertClearsPaused(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.alwaysFail = true
	p := newTestPool(t, be)

	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 2}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	for i := 0; i < maxConsecutiveFail; i++ {
		_, _ = p.CheckOnce("acc_1")
	}
	if cfg, _ := p.Get("acc_1"); !cfg.Paused {
		t.Fatal("前置条件:应已暂停")
	}

	be.alwaysFail = false
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 2}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	cfg, _ := p.Get("acc_1")
	if cfg.Paused || cfg.LastError != "" {
		t.Fatalf("重新保存应解除暂停,实际 %+v", cfg)
	}
}

// TestValidateRejectsOutOfRange 验证配置边界。
func TestValidateRejectsOutOfRange(t *testing.T) {
	cases := []Config{
		{AccountID: "", Target: 5},
		{AccountID: "acc_1", Target: 0},
		{AccountID: "acc_1", Target: MaxTarget + 1},
		{AccountID: "acc_1", Target: 5, CountMode: "weird"},
		{AccountID: "acc_1", Target: 5, MaxPerRun: MaxPerRunLimit + 1},
		{AccountID: "acc_1", Target: 5, IntervalSec: MinIntervalSec - 1},
		{AccountID: "acc_1", Target: 5, GroupID: strings.Repeat("g", 65)},
	}
	for i, c := range cases {
		if _, err := validate(c); err == nil {
			t.Fatalf("第 %d 个非法配置应被拒绝: %+v", i, c)
		}
	}
}

// TestUpsertRejectsUnknownAccount 验证不给不存在的账号配补号。
func TestUpsertRejectsUnknownAccount(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_x", Target: 5}); err == nil {
		t.Fatal("账号不存在时应报错")
	}
}

// TestPersistence 验证配置在重启后仍在。
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	be := newFakeBackend("acc_1")

	p1, err := New(dir, be)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p1.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 8, GroupID: "g_pool"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	p1.Close()

	p2, err := New(dir, be)
	if err != nil {
		t.Fatalf("重开: %v", err)
	}
	defer p2.Close()
	cfg, ok := p2.Get("acc_1")
	if !ok {
		t.Fatal("配置应被持久化")
	}
	if cfg.Target != 8 || !cfg.Enabled || cfg.GroupID != "g_pool" {
		t.Fatalf("配置内容不一致: %+v", cfg)
	}
}

// TestRemove 验证删除配置后不再调度。
func TestRemove(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 3}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !p.Remove("acc_1") {
		t.Fatal("Remove 应返回 true")
	}
	if _, err := p.CheckOnce("acc_1"); err != ErrNotConfigured {
		t.Fatalf("删除后应返回 ErrNotConfigured,实际 %v", err)
	}
	if p.Remove("acc_1") {
		t.Fatal("重复删除应返回 false")
	}
}

// TestListAndCount 验证列表与统计口径。
func TestListAndCount(t *testing.T) {
	list := []hme.Alias{{Active: true}, {Active: true}, {Active: false}}
	if n := countAliases(list, "active"); n != 2 {
		t.Fatalf("active 口径应为 2,实际 %d", n)
	}
	if n := countAliases(list, "all"); n != 3 {
		t.Fatalf("all 口径应为 3,实际 %d", n)
	}
}

// TestTriggerDeduplicates 验证手动触发与自动检查互斥。
func TestTriggerDeduplicates(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 3}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// 模拟一个正在进行中的检查,手动触发应被拒绝
	p.mu.Lock()
	p.inflight["acc_1"] = true
	p.mu.Unlock()
	if err := p.Trigger("acc_1"); err != ErrBusy {
		t.Fatalf("在途时应返回 ErrBusy,实际 %v", err)
	}

	p.mu.Lock()
	delete(p.inflight, "acc_1")
	p.mu.Unlock()
	if err := p.Trigger("acc_1"); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
}

// TestAutoCreateSendsNoLabel 验证自动补号不给 iCloud 传标签。
// 标签是 iCloud 侧字段,本系统只在手动创建时让用户填;
// 自动创建一律留空,由 iCloud 补默认值,归类靠本地分组。
func TestAutoCreateSendsNoLabel(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 3, MaxPerRun: 3}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, err := p.CheckOnce("acc_1")
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if res.Created != 3 {
		t.Fatalf("应创建 3 个,实际 %d", res.Created)
	}
	for i, label := range be.created {
		if label != "" {
			t.Fatalf("第 %d 次创建不应传标签,实际 %q", i+1, label)
		}
	}
}

// TestAutoCreateAssignsGroup 验证配置了分组后,新别名会被写入本地元数据。
func TestAutoCreateAssignsGroup(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{
		AccountID: "acc_1", Enabled: true, Target: 2, MaxPerRun: 2, GroupID: "g_auto",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, err := p.CheckOnce("acc_1")
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if len(res.Emails) != 2 {
		t.Fatalf("应返回 2 个新邮箱,实际 %v", res.Emails)
	}
	for _, email := range res.Emails {
		if got := be.assigned[email]; got != "g_auto" {
			t.Fatalf("%s 应归入 g_auto,实际 %q", email, got)
		}
	}
}

// TestUpsertRaiseTargetRechecksImmediately 验证调大目标或重新启用时
// 会清掉上次检查时间,让下一轮心跳立刻补,而不是干等一个间隔。
func TestUpsertRaiseTargetRechecksImmediately(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)

	first, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 5, IntervalSec: 3600})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// 跑一次让 LastCheckAt 有值
	if _, err := p.CheckOnce("acc_1"); err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	cfg, _ := p.Get("acc_1")
	if cfg.LastCheckAt == "" {
		t.Fatal("前置条件:检查后应有 LastCheckAt")
	}

	// 目标不变:保留时间,不额外打扰上游
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: first.Target, IntervalSec: 3600}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if again, _ := p.Get("acc_1"); again.LastCheckAt == "" {
		t.Fatal("目标不变时应保留上次检查时间")
	}

	// 调大目标:清空,下一轮立即检查
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 20, IntervalSec: 3600}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	raised, _ := p.Get("acc_1")
	if raised.LastCheckAt != "" {
		t.Fatalf("调大目标后应立即重试,实际 LastCheckAt=%q", raised.LastCheckAt)
	}

	// 关闭后再启用:同样清空
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: false, Target: 20, IntervalSec: 3600}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	_, _ = p.CheckOnce("acc_1")
	if _, err := p.Upsert(Config{AccountID: "acc_1", Enabled: true, Target: 20, IntervalSec: 3600}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if re, _ := p.Get("acc_1"); re.LastCheckAt != "" {
		t.Fatalf("重新启用后应立即重试,实际 LastCheckAt=%q", re.LastCheckAt)
	}
}
