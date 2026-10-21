package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// newAsyncTestManager 创建一个临时目录下的管理器,并把网络校验替换为可控实现。
func newAsyncTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

// waitForStatus 轮询等待账号进入期望状态。
func waitForStatus(t *testing.T, m *Manager, id, want string) *Account {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if acc, ok := m.GetAccount(id); ok && acc.Status == want {
			return acc
		}
		time.Sleep(5 * time.Millisecond)
	}
	acc, _ := m.GetAccount(id)
	status := "<不存在>"
	if acc != nil {
		status = acc.Status
	}
	t.Fatalf("等待状态 %q 超时,当前为 %q", want, status)
	return nil
}

// waitValidationDone 等待指定账号的后台校验任务退出。
func waitValidationDone(t *testing.T, m *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		busy := m.inflight[id]
		m.mu.RUnlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("后台校验未在超时内结束")
}

// TestAddAccountWithCookiesReturnsImmediately 验证添加账号不再同步等待上游:
// 接口立即返回 validating,校验结果由后台回写。
func TestAddAccountWithCookiesReturnsImmediately(t *testing.T) {
	m := newAsyncTestManager(t)

	gate := make(chan struct{})
	m.validateFn = func(a *Account) {
		<-gate
		a.Status = "active"
		a.RealEmail = "real@icloud.com"
		a.LastValidated = time.Now().Format(time.RFC3339)
		a.AliasTotal = 5
		a.AliasActive = 3
	}

	start := time.Now()
	sum, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "主号",
		ICloudEmail: "a@icloud.com",
		CookieInput: `{"a":"1"}`,
	})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("添加耗时 %s,应立返回", elapsed)
	}
	if sum.Status != "validating" {
		t.Fatalf("返回状态应为 validating,得到 %q", sum.Status)
	}
	if sum.StatusMessage == "" {
		t.Fatal("validating 状态应带提示文案")
	}

	close(gate)
	acc := waitForStatus(t, m, sum.ID, "active")
	if acc.AliasTotal != 5 || acc.AliasActive != 3 {
		t.Fatalf("别名统计未回写: total=%d active=%d", acc.AliasTotal, acc.AliasActive)
	}
}

// TestBackgroundValidationPersistsResult 验证后台校验结果会落盘。
func TestBackgroundValidationPersistsResult(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	m.validateFn = func(a *Account) {
		a.Status = "error"
		a.LastError = "会话已失效"
	}

	sum, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "副号",
		ICloudEmail: "b@icloud.com",
		CookieInput: "x=1",
	})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	waitForStatus(t, m, sum.ID, "error")
	waitValidationDone(t, m, sum.ID)

	raw, err := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatalf("读取 accounts.json: %v", err)
	}
	var wrapper struct {
		Accounts map[string]*Account `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("解析 accounts.json: %v", err)
	}
	got, ok := wrapper.Accounts[sum.ID]
	if !ok {
		t.Fatalf("accounts.json 缺少账号 %s", sum.ID)
	}
	if got.Status != "error" || got.LastError == "" {
		t.Fatalf("落盘状态未更新: %+v", got)
	}
}

// TestNoCookiesSkipsValidation 验证无 Cookie 的账号不会被排队校验。
func TestNoCookiesSkipsValidation(t *testing.T) {
	m := newAsyncTestManager(t)
	var calls int32
	m.validateFn = func(*Account) { atomic.AddInt32(&calls, 1) }

	sum, err := m.AddAccountWithInput(AddAccountInput{Name: "空号", ICloudEmail: "c@icloud.com"})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	if sum.Status != "pending" {
		t.Fatalf("无 Cookie 应为 pending,得到 %q", sum.Status)
	}
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("无 Cookie 不应触发校验,实际调用 %d 次", n)
	}
}

// TestValidationResultDiscardedWhenCookiesChanged 验证校验期间改了 Cookie 时,
// 旧校验结果不会覆盖新凭据。
func TestValidationResultDiscardedWhenCookiesChanged(t *testing.T) {
	m := newAsyncTestManager(t)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	m.validateFn = func(a *Account) {
		started <- struct{}{}
		<-gate
		a.Status = "active"
		a.Cookies = map[string]string{"a": "1"}
	}

	sum, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "改号",
		ICloudEmail: "d@icloud.com",
		CookieInput: `{"a":"1"}`,
	})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	<-started

	// 模拟校验进行中用户换了一套 Cookie
	m.mu.Lock()
	m.accounts[sum.ID].Cookies = map[string]string{"a": "2"}
	m.mu.Unlock()

	close(gate)
	waitValidationDone(t, m, sum.ID)

	acc, ok := m.GetAccount(sum.ID)
	if !ok {
		t.Fatal("账号应仍然存在")
	}
	if acc.Status == "active" {
		t.Fatal("基于旧 Cookie 的校验结果不应覆盖新凭据状态")
	}
	if acc.Cookies["a"] != "2" {
		t.Fatalf("新 Cookie 被覆盖: %v", acc.Cookies)
	}
}

// TestStartValidationDeduplicates 验证同一账号在途校验期间的重复调度
// 只会补跑一轮,而不是每次调度都排队。
func TestStartValidationDeduplicates(t *testing.T) {
	m := newAsyncTestManager(t)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls int32
	m.validateFn = func(a *Account) {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-gate
		a.Status = "active"
	}

	sum, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "去重号",
		ICloudEmail: "e@icloud.com",
		CookieInput: "k=v",
	})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	<-started
	for i := 0; i < 5; i++ {
		m.startValidation(sum.ID)
	}
	close(gate)
	waitValidationDone(t, m, sum.ID)

	// 首轮 1 次 + 合并后的补跑 1 次;5 次调度不应变成 5 次校验
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("重复调度应合并为首轮 + 一轮补跑,实际 %d 次", n)
	}
	if acc, ok := m.GetAccount(sum.ID); !ok || acc.Status != "active" {
		t.Fatalf("最终状态应为 active,得到 %+v", acc)
	}
}

// TestCloseWaitsForInflightValidation 验证 Close 会等待在途校验收尾。
func TestCloseWaitsForInflightValidation(t *testing.T) {
	m := newAsyncTestManager(t)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	m.validateFn = func(a *Account) {
		started <- struct{}{}
		<-gate
		a.Status = "active"
	}

	if _, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "关闭号",
		ICloudEmail: "f@icloud.com",
		CookieInput: "k=v",
	}); err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	<-started

	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()

	// 校验卡在 gate 上时 Close 应先等待,不能立刻返回
	select {
	case <-closed:
		t.Fatal("Close 不应在在途校验结束前返回")
	case <-time.After(50 * time.Millisecond):
	}

	close(gate)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close 未在超时内返回")
	}
}

// TestLoadNormalizesStaleValidating 验证启动时残留的 validating 会被回收为 pending。
func TestLoadNormalizesStaleValidating(t *testing.T) {
	dir := t.TempDir()
	raw := `{"accounts":{"acc_1":{"id":"acc_1","name":"残留","status":"validating","cookies":{"a":"1"},"created_at":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("准备账号文件: %v", err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	acc, ok := m.GetAccount("acc_1")
	if !ok {
		t.Fatal("账号未加载")
	}
	if acc.Status != "pending" {
		t.Fatalf("残留 validating 应回收为 pending,得到 %q", acc.Status)
	}
}

// TestUpdateCookiesReturnsImmediately 验证保存 Cookie 不再同步等待上游校验。
func TestUpdateCookiesReturnsImmediately(t *testing.T) {
	m := newAsyncTestManager(t)

	sum, err := m.AddAccountWithInput(AddAccountInput{Name: "换 Cookie", ICloudEmail: "g@icloud.com"})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}

	gate := make(chan struct{})
	m.validateFn = func(a *Account) {
		<-gate
		a.Status = "active"
	}

	start := time.Now()
	if err := m.UpdateCookies(sum.ID, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("UpdateCookies: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("保存 Cookie 耗时 %s,应立即返回", elapsed)
	}
	acc, ok := m.GetAccount(sum.ID)
	if !ok || acc.Status != "validating" {
		t.Fatalf("保存后应为 validating,得到 %+v", acc)
	}

	close(gate)
	waitForStatus(t, m, sum.ID, "active")
}

// TestUpdateCookiesWhileValidatingRevalidates 验证校验在途时换 Cookie,
// 会在当前轮结束后自动补跑一轮,不会永久卡在 validating。
func TestUpdateCookiesWhileValidatingRevalidates(t *testing.T) {
	m := newAsyncTestManager(t)

	gate := make(chan struct{})
	started := make(chan int32, 4)
	var calls int32
	m.validateFn = func(a *Account) {
		n := atomic.AddInt32(&calls, 1)
		started <- n
		if n == 1 {
			<-gate
		}
		a.Status = "active"
	}

	sum, err := m.AddAccountWithInput(AddAccountInput{
		Name:        "中途换号",
		ICloudEmail: "h@icloud.com",
		CookieInput: `{"a":"1"}`,
	})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}
	<-started // 第一轮已开始

	if err := m.UpdateCookies(sum.ID, map[string]string{"a": "2"}); err != nil {
		t.Fatalf("UpdateCookies: %v", err)
	}
	close(gate) // 放行第一轮;它基于旧 Cookie,结果应被丢弃

	if n := <-started; n != 2 {
		t.Fatalf("应自动补跑第二轮,实际第 %d 轮", n)
	}
	acc := waitForStatus(t, m, sum.ID, "active")
	if acc.Cookies["a"] != "2" {
		t.Fatalf("补跑应使用新 Cookie,实际 %v", acc.Cookies)
	}
	waitValidationDone(t, m, sum.ID)
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("总校验次数应为 2,实际 %d", n)
	}
}

// TestRemoveAccountRollsBackOnSaveFailure 验证落盘失败时不会只删内存:
// 否则重启后账号会"复活"。
func TestRemoveAccountRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	sum, err := m.AddAccountWithInput(AddAccountInput{Name: "删号", ICloudEmail: "i@icloud.com"})
	if err != nil {
		t.Fatalf("AddAccountWithInput: %v", err)
	}

	// 指向不存在的子目录,让后续落盘必然失败
	m.mu.Lock()
	m.dataFile = filepath.Join(dir, "missing", "accounts.json")
	m.mu.Unlock()

	found, err := m.RemoveAccount(sum.ID)
	if !found {
		t.Fatal("账号存在时应返回 true")
	}
	if err == nil {
		t.Fatal("落盘失败应返回错误")
	}
	if _, ok := m.GetAccount(sum.ID); !ok {
		t.Fatal("落盘失败必须回滚内存删除,否则重启后账号会复活")
	}
}
