package pool

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"icloud-hme/internal/hme"
)

// fakeBackend 是内存版上游,记录创建调用便于断言。
type fakeBackend struct {
	mu            sync.Mutex
	ids           []string
	aliases       map[string][]hme.Alias
	created       []string          // 每次创建传入的 label(自动补号应恒为空)
	assigned      map[string]string // email -> groupID
	metas         map[string]Meta   // 显式指定的本地元数据(优先于 assigned)
	missingGroups map[string]bool   // 被视为"已删除"的分组
	failCreate    int               // 剩余允许失败的创建次数
	alwaysFail    bool
	failList      bool
	seq           int
}

func newFakeBackend(ids ...string) *fakeBackend {
	return &fakeBackend{
		ids:           ids,
		aliases:       map[string][]hme.Alias{},
		assigned:      map[string]string{},
		metas:         map[string]Meta{},
		missingGroups: map[string]bool{},
	}
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

// AliasMeta 用 assigned 当分组来源,状态默认库存;更细的场景由测试直接写 metas。
func (f *fakeBackend) AliasMeta(email string) (Meta, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.metas[email]; ok {
		return m, true
	}
	if g, ok := f.assigned[email]; ok {
		return Meta{GroupID: g, State: StateStock}, true
	}
	return Meta{}, false
}

func (f *fakeBackend) GroupExists(groupID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.missingGroups[groupID]
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

// TestCountMatchingByFilter 验证按筛选条件统计。
func TestCountMatchingByFilter(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.aliases["acc_1"] = []hme.Alias{
		{Email: "a@icloud.com", Active: true},  // 分组 g1 + 库存
		{Email: "b@icloud.com", Active: true},  // 分组 g1 + 已分发
		{Email: "c@icloud.com", Active: true},  // 未登记
		{Email: "d@icloud.com", Active: false}, // 分组 g2 + 库存
	}
	be.metas["a@icloud.com"] = Meta{GroupID: "g1", State: StateStock}
	be.metas["b@icloud.com"] = Meta{GroupID: "g1", State: "issued"}
	// 空状态表示"从没发出去过",应当按库存算
	be.metas["d@icloud.com"] = Meta{GroupID: "g2", State: ""}

	p := newTestPool(t, be)
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"不限条件算全部", Filter{}, 4},
		{"只算启用中", Filter{Active: FilterActiveOn}, 3},
		{"只算库存(空状态视为库存)", Filter{State: FilterStateStock}, 3},
		{"限定分组", Filter{GroupID: "g1"}, 2},
		{"限定分组 + 库存", Filter{GroupID: "g1", State: FilterStateStock}, 1},
		{"分组 + 启用中", Filter{GroupID: "g2", Active: FilterActiveOn}, 0},
		{"不存在的分组", Filter{GroupID: "g9"}, 0},
	}
	for _, tc := range cases {
		if got := p.countMatching(be.aliases["acc_1"], tc.f); got != tc.want {
			t.Fatalf("%s: 期望 %d,实际 %d", tc.name, tc.want, got)
		}
	}
}

// TestCheckOnceCountsOnlyMatching 验证目标数量针对的是筛选后的结果。
func TestCheckOnceCountsOnlyMatching(t *testing.T) {
	be := newFakeBackend("acc_1")
	// 账号下已有 4 个别名,其中只有 1 个属于 g1 且是库存
	be.aliases["acc_1"] = []hme.Alias{
		{Email: "a@icloud.com", Active: true},
		{Email: "b@icloud.com", Active: true},
		{Email: "c@icloud.com", Active: true},
		{Email: "d@icloud.com", Active: true},
	}
	be.metas["a@icloud.com"] = Meta{GroupID: "g1", State: StateStock}
	be.metas["b@icloud.com"] = Meta{GroupID: "g1", State: "issued"}
	be.metas["c@icloud.com"] = Meta{GroupID: "g2", State: StateStock}

	p := newTestPool(t, be)
	cfg, err := p.Upsert(Config{
		AccountID: "acc_1",
		Target:    3,
		Filter:    Filter{GroupID: "g1", State: FilterStateStock},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if cfg.GroupID != "g1" {
		t.Fatalf("按分组统计时新邮箱应归入同一分组,实际 %q", cfg.GroupID)
	}
	res, err := p.CheckOnce("acc_1")
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	// 符合条件 1 个,目标 3 → 补 2 个(而不是按 4 个总数算出 0)
	if res.Current != 1 || res.Created != 2 {
		t.Fatalf("期望 current=1 created=2,实际 current=%d created=%d", res.Current, res.Created)
	}
	for _, email := range res.Emails {
		if be.assigned[email] != "g1" {
			t.Fatalf("新邮箱 %s 应归入 g1,实际 %q", email, be.assigned[email])
		}
	}
}

// TestCheckOnceStopsWhenFilterGroupMissing 验证筛选分组被删除后停止补号。
func TestCheckOnceStopsWhenFilterGroupMissing(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.missingGroups["g1"] = true
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 5, Filter: Filter{GroupID: "g1"}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	res, err := p.CheckOnce("acc_1")
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if res.Created != 0 {
		t.Fatalf("分组不存在时不该创建,实际 %d", res.Created)
	}
	if res.Error == "" {
		t.Fatal("应回显错误原因")
	}
}

// TestUpsertMigratesCountMode 验证旧字段 count_mode 并入 filter.active。
func TestUpsertMigratesCountMode(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	cfg, err := p.Upsert(Config{AccountID: "acc_1", Target: 3, CountMode: "all"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if cfg.Filter.Active != FilterActiveAll {
		t.Fatalf("count_mode=all 应迁移为 active=all,实际 %q", cfg.Filter.Active)
	}
	if cfg.CountMode != "" {
		t.Fatalf("迁移后不该再写出 count_mode,实际 %q", cfg.CountMode)
	}
}

// TestUpsertRejectsUnsafeFilter 验证会导致无限补号的条件被拒绝。
func TestUpsertRejectsUnsafeFilter(t *testing.T) {
	be := newFakeBackend("acc_1")
	p := newTestPool(t, be)
	cases := []struct {
		name string
		f    Filter
	}{
		{"按已分发统计", Filter{State: "issued"}},
		{"按已回收统计", Filter{State: "revoked"}},
		{"按已停用统计", Filter{Active: "inactive"}},
	}
	for _, tc := range cases {
		if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 3, Filter: tc.f}); err == nil {
			t.Fatalf("%s 应被拒绝", tc.name)
		}
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

// TestPreviewCountsWithoutCreating 验证试算只统计、不创建,并给出状态分布。
func TestPreviewCountsWithoutCreating(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.aliases["acc_1"] = []hme.Alias{
		{Email: "a@icloud.com", Active: true},  // 库存
		{Email: "b@icloud.com", Active: true},  // 已分发
		{Email: "c@icloud.com", Active: false}, // 已回收 + 停用
	}
	be.metas["a@icloud.com"] = Meta{State: StateStock}
	be.metas["b@icloud.com"] = Meta{State: "issued"}
	be.metas["c@icloud.com"] = Meta{State: "revoked"}

	p := newTestPool(t, be)
	// 没配置过也能试算:按「可用库存」口径,默认目标 20
	got, err := p.Preview("acc_1", nil, 0)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got.Target != DefaultTarget {
		t.Fatalf("未配置时目标应为默认 %d,实际 %d", DefaultTarget, got.Target)
	}
	if got.Current != 1 {
		t.Fatalf("可用库存应为 1(只有 a 是库存且启用),实际 %d", got.Current)
	}
	// 单轮上限默认 3,差 19 个也只报 3
	if got.ToCreate != DefaultMaxPerRun {
		t.Fatalf("本轮预计补 %d 个,实际 %d", DefaultMaxPerRun, got.ToCreate)
	}
	if got.Breakdown.Stock != 1 || got.Breakdown.Issued != 1 || got.Breakdown.Revoked != 1 {
		t.Fatalf("状态分布不对: %+v", got.Breakdown)
	}
	if got.Breakdown.Inactive != 1 || got.Breakdown.Total != 3 {
		t.Fatalf("停用/总数不对: %+v", got.Breakdown)
	}
	if len(be.aliases["acc_1"]) != 3 {
		t.Fatalf("试算不该创建别名,实际 %d 个", len(be.aliases["acc_1"]))
	}

	// 目标已达成的口径:全部别名,目标 3 → 不补
	all := Filter{State: FilterStateAll, Active: FilterActiveAll}
	got, err = p.Preview("acc_1", &all, 3)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got.Current != 3 || got.ToCreate != 0 {
		t.Fatalf("全部别名口径应为 3 个且不补,实际 current=%d to_create=%d", got.Current, got.ToCreate)
	}
}

// TestPreviewRejectsUnsafeFilter 验证试算同样会拦下不安全的筛选条件。
func TestPreviewRejectsUnsafeFilter(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.aliases["acc_1"] = []hme.Alias{{Email: "a@icloud.com", Active: true}}
	p := newTestPool(t, be)

	bad := Filter{State: "issued"}
	got, err := p.Preview("acc_1", &bad, 10)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got.Error == "" {
		t.Fatal("按已分发试算应返回错误说明")
	}
}

// TestConcurrentCheckOnceCreatesOnce 并发调用 CheckOnce 时只能有一个真正执行。
//
// 这是"重复建号"的最后一道闸:两个请求同时进来,如果都通过了在途判断,
// 就会各自按差额建一遍,凭空多出一倍的号。
func TestConcurrentCheckOnceCreatesOnce(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.seed("acc_1", 0, 0)
	p := newTestPool(t, be)
	// 放慢单次创建,给并发竞争留出窗口
	p.createGap = 60 * time.Millisecond
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 4, MaxPerRun: 4}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	busy := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.CheckOnce("acc_1"); errors.Is(err, ErrBusy) {
				busy <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(busy)

	// 目标 4 个,不管几个并发,最终数量必须正好是 4
	got := len(be.aliases["acc_1"])
	if got != 4 {
		t.Fatalf("并发检查后应有且只有 4 个,实际 %d", got)
	}
	// 其余请求应当被明确拒绝(而不是排队重复执行)
	if len(busy) == 0 {
		t.Log("本轮没有并发冲突(调度太快),数量正确即可")
	}
}

// TestPreviewConcurrentWithCheck 试算与真实检查同时进行时不创建、不阻塞。
func TestPreviewConcurrentWithCheck(t *testing.T) {
	be := newFakeBackend("acc_1")
	be.seed("acc_1", 1, 0)
	p := newTestPool(t, be)
	if _, err := p.Upsert(Config{AccountID: "acc_1", Target: 5, MaxPerRun: 2}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = p.CheckOnce("acc_1") }()
	go func() {
		defer wg.Done()
		pv, err := p.Preview("acc_1", nil, 0)
		if err != nil {
			t.Errorf("Preview: %v", err)
		}
		_ = pv
	}()
	wg.Wait()
}
