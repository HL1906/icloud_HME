package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authedDo 带上会话与 CSRF 发起请求。
func authedDo(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, method, path, body)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	status, raw, _ := do(t, req)
	var out map[string]any
	_ = json.Unmarshal([]byte(raw), &out)
	return status, out
}

func TestForwardRequiresAlias(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, out := authedDo(t, ts, "GET", "/api/forward?account_id=acc_1", "")
	if status != http.StatusBadRequest {
		t.Fatalf("缺少 alias 期望 400,得到 %d", status)
	}
	if out["code"] != "VALIDATION_ERROR" {
		t.Fatalf("期望 VALIDATION_ERROR,得到 %v", out["code"])
	}
}

func TestForwardRequiresAccount(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, _ := authedDo(t, ts, "GET", "/api/forward?alias=a@icloud.com", "")
	if status != http.StatusBadRequest {
		t.Fatalf("缺少 account_id 期望 400,得到 %d", status)
	}
}

func TestForwardNeedsSession(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, raw, _ := do(t, authedReq(t, ts, "GET", "/api/forward?account_id=acc_1&alias=a@icloud.com", ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录期望 401,得到 %d (%s)", status, raw)
	}
}

func TestForwardPassesQueryAndReturnsMessages(t *testing.T) {
	f := &fakeBackend{forwardResult: ForwardResult{
		AccountID: "acc_1",
		Alias:     "a@icloud.com",
		Count:     1,
		Method:    "imap",
		Messages: []ForwardMessage{
			{ID: "42", From: "noreply@example.com", Subject: "验证码", Preview: "123456"},
		},
	}}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, out := authedDo(t, ts, "GET",
		"/api/forward?account_id=acc_1&alias=a@icloud.com&limit=5&days=3&include_body=1", "")
	if status != http.StatusOK {
		t.Fatalf("期望 200,得到 %d (%v)", status, out)
	}
	if f.forwardQuery.Limit != 5 || f.forwardQuery.Days != 3 {
		t.Fatalf("limit/days 未透传: %+v", f.forwardQuery)
	}
	if !f.forwardQuery.IncludeBody {
		t.Fatal("include_body 未透传")
	}
	if f.forwardQuery.Alias != "a@icloud.com" {
		t.Fatalf("alias 未透传: %q", f.forwardQuery.Alias)
	}

	data, _ := out["data"].(map[string]any)
	if data["alias"] != "a@icloud.com" {
		t.Fatalf("响应 alias 不符: %v", data["alias"])
	}
	if data["method"] != "imap" {
		t.Fatalf("响应 method 不符: %v", data["method"])
	}
}

func TestForwardInvalidLimit(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, _ := authedDo(t, ts, "GET", "/api/forward?account_id=acc_1&alias=a@icloud.com&limit=0", "")
	if status != http.StatusBadRequest {
		t.Fatalf("limit=0 期望 400,得到 %d", status)
	}
}

func TestAckForwardMarksDone(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	body := `{"account_id":"acc_1","alias":"a@icloud.com","ids":["42","43"]}`
	status, out := authedDo(t, ts, "POST", "/api/forward/ack", body)
	if status != http.StatusOK {
		t.Fatalf("期望 200,得到 %d (%v)", status, out)
	}
	if len(f.ackedIDs) != 2 {
		t.Fatalf("应标记 2 封,实际 %v", f.ackedIDs)
	}
	if !f.ackedForwarded {
		t.Fatal("默认应标记为已转发")
	}
}

func TestAckForwardSupportsUnmark(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	body := `{"account_id":"acc_1","alias":"a@icloud.com","ids":["42"],"forwarded":false}`
	status, _ := authedDo(t, ts, "POST", "/api/forward/ack", body)
	if status != http.StatusOK {
		t.Fatalf("期望 200,得到 %d", status)
	}
	if f.ackedForwarded {
		t.Fatal("forwarded=false 应取消标记")
	}
}

func TestAckForwardRejectsEmptyIDs(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	body := `{"account_id":"acc_1","alias":"a@icloud.com","ids":[]}`
	status, _ := authedDo(t, ts, "POST", "/api/forward/ack", body)
	if status != http.StatusBadRequest {
		t.Fatalf("空 ids 期望 400,得到 %d", status)
	}
}

func TestAckForwardNeedsCSRF(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	session, _ := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, "POST", "/api/forward/ack",
		`{"account_id":"acc_1","alias":"a@icloud.com","ids":["1"]}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	status, raw, _ := do(t, req)
	if status == http.StatusOK {
		t.Fatalf("缺少 CSRF 不应成功: %d (%s)", status, raw)
	}
	if len(f.ackedIDs) != 0 {
		t.Fatal("缺少 CSRF 时不应发生标记")
	}
}

func TestGetMessageAcceptsNonNumericID(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	// Web API 的 threadId 不是数字,不应再直接 400
	status, out := authedDo(t, ts, "GET",
		"/api/inbox/AAMkAGI2THREAD?account_id=acc_1", "")
	if status != http.StatusOK {
		t.Fatalf("非数字 ID 期望 200,得到 %d (%v)", status, out)
	}
	if !strings.Contains(f.gotMessageID, "AAMkAGI2THREAD") {
		t.Fatalf("ID 应原样透传,实际 %q", f.gotMessageID)
	}
}

func TestGetMessageRejectsEmptyID(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()

	status, _ := authedDo(t, ts, "GET", "/api/inbox/%20?account_id=acc_1", "")
	if status != http.StatusBadRequest {
		t.Fatalf("空白 ID 期望 400,得到 %d", status)
	}
}
