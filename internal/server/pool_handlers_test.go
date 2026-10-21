package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/account"
)

// newPoolTestServer 构造启用补号调度的测试 Server(需要真实 DataDir)。
func newPoolTestServer(t *testing.T, f *fakeBackend) *httptest.Server {
	t.Helper()
	s, err := newWithBackend(f, Config{
		AdminPassword: "admin-pass-2026-strong",
		SessionTTL:    12 * time.Hour,
		DataDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("newWithBackend: %v", err)
	}
	t.Cleanup(s.Close)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestPoolHandlersFlow 覆盖配置的增改查删与手动触发。
func TestPoolHandlersFlow(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "GET", "/api/pool", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("初始列表: %d %s", code, body)
	}
	if !strings.Contains(body, `"configs":[]`) && !strings.Contains(body, `"configs":null`) {
		t.Fatalf("初始应为空列表: %s", body)
	}

	// 新增配置
	req = authedReq(t, ts, "PUT", "/api/pool/acc_1",
		`{"enabled":true,"target":6,"count_mode":"active","max_per_run":2,"interval_sec":600}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("保存配置: %d %s", code, body)
	}
	if !strings.Contains(body, `"target":6`) || !strings.Contains(body, `"interval_sec":600`) {
		t.Fatalf("响应应回显配置: %s", body)
	}

	// 列表能读到
	req = authedReq(t, ts, "GET", "/api/pool", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, `"account_id":"acc_1"`) {
		t.Fatalf("列表应包含账号: %d %s", code, body)
	}

	// 手动触发
	req = authedReq(t, ts, "POST", "/api/pool/acc_1/run", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, `"triggered":true`) {
		t.Fatalf("触发: %d %s", code, body)
	}

	// 删除配置
	req = authedReq(t, ts, "DELETE", "/api/pool/acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("删除: %d %s", code, body)
	}

	// 再删应 404
	req = authedReq(t, ts, "DELETE", "/api/pool/acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, _, _ = do(t, req)
	if code != http.StatusNotFound {
		t.Fatalf("重复删除应 404,实际 %d", code)
	}
}

// TestPoolHandlersValidation 验证参数边界与不存在的账号。
func TestPoolHandlersValidation(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	cases := []struct {
		name string
		path string
		body string
	}{
		{"目标为 0", "/api/pool/acc_1", `{"target":0}`},
		{"目标超上限", "/api/pool/acc_1", `{"target":999}`},
		{"间隔太短", "/api/pool/acc_1", `{"target":5,"interval_sec":10}`},
		{"计数口径非法", "/api/pool/acc_1", `{"target":5,"count_mode":"wat"}`},
		{"账号不存在", "/api/pool/acc_x", `{"target":5}`},
		{"非法 JSON", "/api/pool/acc_1", `{`},
	}
	for _, c := range cases {
		req := authedReq(t, ts, "PUT", c.path, c.body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		code, body, _ := do(t, req)
		if code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400,实际 %d %s", c.name, code, body)
		}
	}
}

// TestPoolHandlersUnavailable 验证没有数据目录时接口明确报不可用,
// 而不是静默成功。
func TestPoolHandlersUnavailable(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	s, err := newWithBackend(f, Config{
		AdminPassword: "admin-pass-2026-strong",
		SessionTTL:    12 * time.Hour,
	})
	if err != nil {
		t.Fatalf("newWithBackend: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	session, _ := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "GET", "/api/pool", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ := do(t, req)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("应返回 503,实际 %d %s", code, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if out.Code != "POOL_UNAVAILABLE" {
		t.Fatalf("错误码应为 POOL_UNAVAILABLE,实际 %q(原始 %s)", out.Code, body)
	}
}
