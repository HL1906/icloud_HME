package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestLoginNotLockedByAttacker 验证:攻击者刷错误密码打满限流后,
// 管理员用正确密码**仍然可以登录**(反向代理部署下的登录 DoS 回归用例)。
func TestLoginNotLockedByAttacker(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	// 攻击者把失败次数打满
	for i := 0; i < 20; i++ {
		req := authedReq(t, ts, "POST", "/api/auth/login", `{"password":"wrong-password"}`)
		status, _, _ := do(t, req)
		if status != http.StatusUnauthorized && status != http.StatusTooManyRequests {
			t.Fatalf("意外状态: %d", status)
		}
	}

	// 管理员用正确密码登录必须成功
	body := fmt.Sprintf(`{"password":%q}`, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/auth/login", body)
	status, raw, cookies := do(t, req)
	if status != http.StatusOK {
		t.Fatalf("管理员正确密码应能登录,实际 %d: %s", status, raw)
	}
	hasSession := false
	for _, c := range cookies {
		if c.Name == "hme_session" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Fatal("登录成功应下发会话 Cookie")
	}
}

// TestLoginRejectsOversizedPassword 验证超长密码被拒绝,不会拖慢 argon2。
func TestLoginRejectsOversizedPassword(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'a'
	}
	body := fmt.Sprintf(`{"password":%q}`, string(long))
	req := authedReq(t, ts, "POST", "/api/auth/login", body)
	status, _, _ := do(t, req)
	if status != http.StatusBadRequest {
		t.Fatalf("超长密码期望 400,得到 %d", status)
	}
}

// TestSecurityHeaders 验证关键安全响应头齐全。
func TestSecurityHeaders(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()
	_ = s

	resp, err := http.Get(ts.URL + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("响应头 %s = %q, 期望 %q", k, got, v)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, part := range []string{"object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP 缺少 %q: %s", part, csp)
		}
	}
}

// TestCSRFRejectsCrossOrigin 验证跨源 Origin 被拒绝。
func TestCSRFRejectsCrossOrigin(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/forward/ack",
		`{"account_id":"acc_1","alias":"a@icloud.com","ids":["1"]}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", "https://evil.example.com")

	status, _, _ := do(t, req)
	if status != http.StatusForbidden {
		t.Fatalf("跨源请求期望 403,得到 %d", status)
	}
}

// TestBodyLimitRejectsHugePayload 验证超大请求体被拒绝。
func TestBodyLimitRejectsHugePayload(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	big := make([]byte, 3<<20) // 3 MiB > 1 MiB 上限
	for i := range big {
		big[i] = 'x'
	}
	req := authedReq(t, ts, "POST", "/api/create", fmt.Sprintf(`{"account_id":"acc_1","label":%q}`, string(big)))
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)

	status, _, _ := do(t, req)
	if status == http.StatusOK {
		t.Fatal("超大请求体不应被接受")
	}
}
