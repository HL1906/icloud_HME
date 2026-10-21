package server

import (
	"net/http"
	"testing"
)

// TestChangePasswordSuccess 验证:登录后可以用旧密码换新密码,且新密码能登录、旧密码失效。
func TestChangePasswordSuccess(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	body := `{"old_password":"admin-pass-2026-strong","new_password":"NewPass2026xyz"}`
	req := authedReq(t, ts, "POST", "/api/auth/password", body)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)

	status, raw, _ := do(t, req)
	if status != http.StatusOK {
		t.Fatalf("修改密码期望 200,得到 %d: %s", status, raw)
	}

	// 新密码可登录
	loginReq := authedReq(t, ts, "POST", "/api/auth/login", `{"password":"NewPass2026xyz"}`)
	if st, _, _ := do(t, loginReq); st != http.StatusOK {
		t.Fatalf("新密码应能登录,得到 %d", st)
	}
	// 旧密码失效
	oldReq := authedReq(t, ts, "POST", "/api/auth/login", `{"password":"admin-pass-2026-strong"}`)
	if st, _, _ := do(t, oldReq); st == http.StatusOK {
		t.Fatal("旧密码不应还能登录")
	}
}

// TestChangePasswordWrongOld 验证旧密码错误被拒绝。
func TestChangePasswordWrongOld(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/auth/password",
		`{"old_password":"wrong-old-pass","new_password":"NewPass2026xyz"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)

	status, _, _ := do(t, req)
	if status != http.StatusUnauthorized {
		t.Fatalf("旧密码错误期望 401,得到 %d", status)
	}

	// 原密码仍有效
	loginReq := authedReq(t, ts, "POST", "/api/auth/login", `{"password":"admin-pass-2026-strong"}`)
	if st, _, _ := do(t, loginReq); st != http.StatusOK {
		t.Fatalf("原密码应仍然有效,得到 %d", st)
	}
}

// TestChangePasswordWeakNew 验证弱密码被拒绝。
func TestChangePasswordWeakNew(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	for _, weak := range []string{"short", "abcdefghij", "1234567890"} {
		body := `{"old_password":"admin-pass-2026-strong","new_password":"` + weak + `"}`
		req := authedReq(t, ts, "POST", "/api/auth/password", body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		status, _, _ := do(t, req)
		if status != http.StatusBadRequest {
			t.Fatalf("弱密码 %q 期望 400,得到 %d", weak, status)
		}
	}
}

// TestChangePasswordRequiresAuth 验证未登录无法改密码。
func TestChangePasswordRequiresAuth(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	req := authedReq(t, ts, "POST", "/api/auth/password",
		`{"old_password":"admin-pass-2026-strong","new_password":"NewPass2026xyz"}`)
	status, _, _ := do(t, req)
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录期望 401,得到 %d", status)
	}
}

// TestChangePasswordRequiresCSRF 验证缺少 CSRF 时无法改密码。
func TestChangePasswordRequiresCSRF(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, _ := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/auth/password",
		`{"old_password":"admin-pass-2026-strong","new_password":"NewPass2026xyz"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})

	status, _, _ := do(t, req)
	if status != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 期望 403,得到 %d", status)
	}
}

// TestChangePasswordSameAsOld 验证新密码不能与旧密码相同。
func TestChangePasswordSameAsOld(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/auth/password",
		`{"old_password":"admin-pass-2026-strong","new_password":"admin-pass-2026-strong"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)

	status, _, _ := do(t, req)
	if status != http.StatusBadRequest {
		t.Fatalf("新密码与旧密码相同期望 400,得到 %d", status)
	}
}
