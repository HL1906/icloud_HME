package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

// scanResponse 是扫描接口的响应体(只取测试关心的字段)。
type scanResponse struct {
	Data struct {
		AccountID  string `json:"account_id"`
		Method     string `json:"method"`
		Scanned    int    `json:"scanned"`
		Matched    int    `json:"matched"`
		Updated    int    `json:"updated"`
		WindowDays int    `json:"window_days"`
		LastScanAt string `json:"last_scan_at"`
	} `json:"data"`
}

func TestScanActivityAppliesHits(t *testing.T) {
	hit := time.Now().Add(-3 * 24 * time.Hour).UTC().Truncate(time.Second)
	f := &fakeBackend{
		accounts: []account.Summary{{ID: "acc_1", Name: "主号"}},
		aliases: []hme.Alias{
			{Email: "a@icloud.com", AnonymousID: "anon_a", Active: true},
			{Email: "b@icloud.com", AnonymousID: "anon_b", Active: true},
		},
		activity: AliasActivityScan{
			Method:  "imap",
			Scanned: 42,
			Hits:    map[string]time.Time{"a@icloud.com": hit},
		},
	}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "POST", "/api/alias-activity/scan", `{"account_id":"acc_1"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("扫描失败: %d %s", code, body)
	}

	var parsed scanResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("解析响应: %v %s", err, body)
	}
	if parsed.Data.Scanned != 42 || parsed.Data.Matched != 1 || parsed.Data.Updated != 1 {
		t.Fatalf("响应统计不符: %+v", parsed.Data)
	}
	if parsed.Data.WindowDays != 90 || parsed.Data.Method != "imap" || parsed.Data.LastScanAt == "" {
		t.Fatalf("默认窗口/方式不符: %+v", parsed.Data)
	}
	// 省略参数时应落到默认值,而不是把 0 传给上游
	if f.activityDays != 90 || f.activityLimit != 500 {
		t.Fatalf("默认参数未生效: days=%d limit=%d", f.activityDays, f.activityLimit)
	}

	// 收信时间应落到本地元数据,并随别名列表返回
	req = authedReq(t, ts, "GET", "/api/aliases?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("别名列表: %d %s", code, body)
	}
	var list struct {
		Data struct {
			Meta map[string]struct {
				LastReceivedAt string `json:"last_received_at"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("解析别名列表: %v %s", err, body)
	}
	if got := list.Data.Meta["a@icloud.com"].LastReceivedAt; got != hit.Format(time.RFC3339) {
		t.Fatalf("别名列表应带收信时间, got %q", got)
	}
	// 没有命中的别名不应凭空出现记录
	if entry, ok := list.Data.Meta["b@icloud.com"]; ok && entry.LastReceivedAt != "" {
		t.Fatalf("未收信的别名不该有记录: %+v", entry)
	}

	// 扫描摘要可单独查询
	req = authedReq(t, ts, "GET", "/api/alias-activity?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("查询扫描状态: %d %s", code, body)
	}
	var act struct {
		Data struct {
			Available bool `json:"available"`
			Activity  struct {
				Scanned    int `json:"scanned"`
				WindowDays int `json:"window_days"`
			} `json:"activity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &act); err != nil {
		t.Fatalf("解析扫描状态: %v %s", err, body)
	}
	if !act.Data.Available || act.Data.Activity.Scanned != 42 || act.Data.Activity.WindowDays != 90 {
		t.Fatalf("扫描摘要不符: %s", body)
	}
}

func TestScanActivityValidation(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	post := func(body string) (int, string) {
		req := authedReq(t, ts, "POST", "/api/alias-activity/scan", body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		code, resp, _ := do(t, req)
		return code, resp
	}

	if code, _ := post(`{}`); code != http.StatusBadRequest {
		t.Fatalf("缺 account_id 应 400, got %d", code)
	}
	if code, _ := post(`{"account_id":"acc_1","days":400}`); code != http.StatusBadRequest {
		t.Fatalf("days 越界应 400, got %d", code)
	}
	if code, _ := post(`{"account_id":"acc_1","limit":5000}`); code != http.StatusBadRequest {
		t.Fatalf("limit 越界应 400, got %d", code)
	}
	// 没带 CSRF 时应在中间件就被拒
	req := authedReq(t, ts, "POST", "/api/alias-activity/scan", `{"account_id":"acc_1"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	if code, _, _ := do(t, req); code != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", code)
	}
	if f.activityCalls != 0 {
		t.Fatalf("参数非法时不该调用上游, got %d", f.activityCalls)
	}
}

func TestScanActivityUpstreamFailure(t *testing.T) {
	f := &fakeBackend{
		activityErr: &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "无可用邮件客户端: 需要 App Password 或 Cookie"},
	}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "POST", "/api/alias-activity/scan", `{"account_id":"acc_1"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ := do(t, req)
	if code != http.StatusBadRequest || !strings.Contains(body, "VALIDATION_ERROR") {
		t.Fatalf("应原样透出后端错误: %d %s", code, body)
	}
}

func TestBeginScanRejectsConcurrent(t *testing.T) {
	s := &Server{scanning: make(map[string]bool)}
	if !s.beginScan("acc_1") {
		t.Fatal("首次应能占用")
	}
	if s.beginScan("acc_1") {
		t.Fatal("同账号第二次应被拒绝")
	}
	if !s.beginScan("acc_2") {
		t.Fatal("不同账号互不影响")
	}
	s.endScan("acc_1")
	if !s.beginScan("acc_1") {
		t.Fatal("释放后应能再次占用")
	}
}
