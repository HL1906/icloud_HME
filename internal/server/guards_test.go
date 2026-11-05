package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

// --- 已失效令牌 -----------------------------------------------------------------

// TestPublicMailShowsRevokedPage 回收后打开旧链接应看到"已失效",而不是 404。
func TestPublicMailShowsRevokedPage(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	s, ts := newTestServer(f)
	defer ts.Close()

	issued, err := s.meta.IssueToken("gone@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.RevokeToken("gone@icloud.com"); err != nil {
		t.Fatal(err)
	}

	// HTML:给出的是"已失效"页面,不是 404
	resp, err := http.Get(ts.URL + "/m?e=gone@icloud.com:" + issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rawBytes, _ := io.ReadAll(resp.Body)
	body := string(rawBytes)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("已失效链接应 410, 实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("应返回 HTML, 实际 %s", ct)
	}
	for _, want := range []string{"这个链接已失效", "联系发件人"} {
		if !strings.Contains(body, want) {
			t.Fatalf("页面缺少 %q:\n%s", want, body)
		}
	}
	// 页面里不该出现邮件列表才有的结构
	if strings.Contains(body, "publicMailTemplate") {
		t.Fatal("不该复用取件页模板")
	}

	// JSON:code=410,便于脚本区分"没这个链接"和"链接过期"
	code, raw, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=gone@icloud.com:"+issued.Token))
	if code != http.StatusGone {
		t.Fatalf("JSON 也应 410, 实际 %d", code)
	}
	var out struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if out.Code != http.StatusGone {
		t.Fatalf("JSON code 应为 410, 实际 %d", out.Code)
	}
}

// TestPublicMailRevokedButWrongEmail 令牌已失效,但邮箱对不上时仍是 404。
//
// 否则只要拿着"某个已失效令牌"就能试出它原来属于哪个邮箱。
func TestPublicMailRevokedButWrongEmail(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	issued, err := s.meta.IssueToken("real@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.RevokeToken("real@icloud.com"); err != nil {
		t.Fatal(err)
	}

	code, _, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=other@icloud.com:"+issued.Token))
	if code != http.StatusNotFound {
		t.Fatalf("邮箱不匹配应 404, 实际 %d", code)
	}
}

// TestRotateMakesOldTokenExpired 换新令牌后,旧链接走"已失效"而不是 404。
func TestRotateMakesOldTokenExpired(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	first, err := s.meta.IssueToken("rot@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.IssueToken("rot@icloud.com", "acc_1", "", true); err != nil {
		t.Fatal(err)
	}

	code, _, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=rot@icloud.com:"+first.Token))
	if code != http.StatusGone {
		t.Fatalf("被轮换的旧链接应 410, 实际 %d", code)
	}
}

// TestSetStateClearsTokenAsExpired 手工改成"已回收"后,原链接也应提示已失效。
func TestSetStateClearsTokenAsExpired(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	issued, err := s.meta.IssueToken("st@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.SetState("st@icloud.com", "revoked"); err != nil {
		t.Fatal(err)
	}

	code, _, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=st@icloud.com:"+issued.Token))
	if code != http.StatusGone {
		t.Fatalf("状态变更后旧链接应 410, 实际 %d", code)
	}
}

// --- 健康检查 -------------------------------------------------------------------

// TestHealthzHealthy 正常状态下返回 200 且不含敏感信息。
func TestHealthzHealthy(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	_, ts := newTestServer(f)
	defer ts.Close()

	code, body, _ := do(t, mustGet(t, ts.URL+"/api/healthz"))
	if code != http.StatusOK {
		t.Fatalf("应 200, 实际 %d (%s)", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if out["status"] != "ok" {
		t.Fatalf("status 应为 ok, 实际 %v", out["status"])
	}
	// 探活接口不该暴露账号信息
	for _, leak := range []string{"主号", "acc_1"} {
		if strings.Contains(body, leak) {
			t.Fatalf("healthz 不应包含 %q: %s", leak, body)
		}
	}
}

// TestHealthzNoAuthRequired 探活接口不需要登录。
func TestHealthzNoAuthRequired(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	// 不带任何 Cookie 直接请求
	code, _, _ := do(t, mustGet(t, ts.URL+"/api/healthz"))
	if code != http.StatusOK {
		t.Fatalf("healthz 不应要求登录, 实际 %d", code)
	}
}

// --- 收件箱参数校验 --------------------------------------------------------------

// TestListInboxRejectsUnknownAccount 账号不存在时直接 404,不透传给上游。
func TestListInboxRejectsUnknownAccount(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")
	_ = csrf

	code, raw := adminDo(t, ts, session, "", "GET", "/api/inbox?account_id=acc_nope", "")
	if code != http.StatusNotFound {
		t.Fatalf("不存在的账号应 404, 实际 %d (%s)", code, raw)
	}
	if !strings.Contains(raw, "账号不存在") {
		t.Fatalf("错误信息应说明账号不存在: %s", raw)
	}
}

// TestListInboxRejectsForeignAlias 别名不属于该账号时 404。
func TestListInboxRejectsForeignAlias(t *testing.T) {
	f := &fakeBackend{
		accounts: []account.Summary{{ID: "acc_1"}},
		aliases:  []hme.Alias{{Email: "mine@icloud.com", Active: true}},
	}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, _ := login(t, ts, "admin-pass-2026-strong")

	code, raw := adminDo(t, ts, session, "",
		"GET", "/api/inbox?account_id=acc_1&alias=someone-else@icloud.com", "")
	if code != http.StatusNotFound {
		t.Fatalf("不属于该账号的别名应 404, 实际 %d (%s)", code, raw)
	}

	// 属于该账号的别名要放行
	code, raw = adminDo(t, ts, session, "",
		"GET", "/api/inbox?account_id=acc_1&alias=mine@icloud.com", "")
	if code != http.StatusOK {
		t.Fatalf("自己的别名应 200, 实际 %d (%s)", code, raw)
	}
}

// TestListAliasesRejectsUnknownAccount 列表接口同样拦下不存在的账号。
func TestListAliasesRejectsUnknownAccount(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, _ := login(t, ts, "admin-pass-2026-strong")

	code, raw := adminDo(t, ts, session, "", "GET", "/api/aliases?account_id=ghost", "")
	if code != http.StatusNotFound {
		t.Fatalf("不存在的账号应 404, 实际 %d (%s)", code, raw)
	}
}
