package server

import (
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

// --- 建号限流 -------------------------------------------------------------------

// TestCreateLimiterAllowsUpToLimit 窗口内允许到上限,之后拒绝。
func TestCreateLimiterAllowsUpToLimit(t *testing.T) {
	l := newCreateLimiter()
	base := time.Now()
	l.now = func() time.Time { return base }

	for i := 0; i < createRateLimit; i++ {
		if ok, _ := l.allow("acc_1"); !ok {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	ok, retry := l.allow("acc_1")
	if ok {
		t.Fatal("超出上限应拒绝")
	}
	if retry <= 0 || retry > createRateWindow {
		t.Fatalf("等待时长应在 (0, %v] 内, 实际 %v", createRateWindow, retry)
	}
}

// TestCreateLimiterSeparateAccounts 不同账号互不影响。
func TestCreateLimiterSeparateAccounts(t *testing.T) {
	l := newCreateLimiter()
	base := time.Now()
	l.now = func() time.Time { return base }

	for i := 0; i < createRateLimit; i++ {
		l.allow("acc_1")
	}
	if ok, _ := l.allow("acc_1"); ok {
		t.Fatal("acc_1 应已被限流")
	}
	if ok, _ := l.allow("acc_2"); !ok {
		t.Fatal("acc_2 不该被 acc_1 的记录影响")
	}
}

// TestCreateLimiterRecoversAfterWindow 窗口滑过后恢复。
func TestCreateLimiterRecoversAfterWindow(t *testing.T) {
	l := newCreateLimiter()
	base := time.Now()
	current := base
	l.now = func() time.Time { return current }

	for i := 0; i < createRateLimit; i++ {
		l.allow("acc_1")
	}
	if ok, _ := l.allow("acc_1"); ok {
		t.Fatal("应被限流")
	}

	// 时间往后推过一个窗口
	current = base.Add(createRateWindow + time.Second)
	if ok, _ := l.allow("acc_1"); !ok {
		t.Fatal("窗口过后应恢复")
	}
}

// TestCreateAliasReturns429 接口层:超限返回 429 且带 Retry-After。
func TestCreateAliasReturns429(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}, created: &hme.CreateResult{Email: "new@icloud.com"}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	body := `{"account_id":"acc_1","label":"x"}`
	var lastCode int
	var lastRaw string
	for i := 0; i < createRateLimit+1; i++ {
		lastCode, lastRaw = adminDo(t, ts, session, csrf, "POST", "/api/create", body)
	}
	if lastCode != 429 {
		t.Fatalf("第 %d 次建号应被限流(429), 实际 %d (%s)", createRateLimit+1, lastCode, lastRaw)
	}
	if !strings.Contains(lastRaw, "请") || !strings.Contains(lastRaw, "秒后再试") {
		t.Fatalf("应提示等待时长: %s", lastRaw)
	}
}

// TestCreateAliasValidationDoesNotConsumeQuota 参数错误不该占用建号额度。
func TestCreateAliasValidationDoesNotConsumeQuota(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}, created: &hme.CreateResult{Email: "new@icloud.com"}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 先用掉一半额度
	for i := 0; i < createRateLimit-1; i++ {
		if code, raw := adminDo(t, ts, session, csrf, "POST", "/api/create", `{"account_id":"acc_1"}`); code != 200 {
			t.Fatalf("前 %d 次应成功, 实际 %d (%s)", i+1, code, raw)
		}
	}
	// 中间穿插若干非法请求
	for i := 0; i < 5; i++ {
		adminDo(t, ts, session, csrf, "POST", "/api/create", `{"label":"no-account"}`)
	}
	// 额度仍在,最后一次应该还能成功
	if code, raw := adminDo(t, ts, session, csrf, "POST", "/api/create", `{"account_id":"acc_1"}`); code != 200 {
		t.Fatalf("非法请求不该消耗额度, 最后一次应成功, 实际 %d (%s)", code, raw)
	}
	// 但下一次就该满了
	if code, _ := adminDo(t, ts, session, csrf, "POST", "/api/create", `{"account_id":"acc_1"}`); code != 429 {
		t.Fatalf("额度用尽后应 429, 实际 %d", code)
	}
}
