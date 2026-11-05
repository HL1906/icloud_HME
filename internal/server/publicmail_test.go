package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icloud-hme/internal/account"
)

// adminDo 发一个带管理员会话与 CSRF 的请求。
func adminDo(t *testing.T, ts *httptest.Server, session, csrf, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	code, raw, _ := do(t, req)
	return code, raw
}

// publicMailJSON 是取件接口的 JSON 响应结构(与 wdmail 风格一致)。
type publicMailJSON struct {
	Code int `json:"code"`
	Data struct {
		Recipient      string      `json:"recipient"`
		MailQueryHours int         `json:"mail_query_hours"`
		Count          int         `json:"count"`
		Messages       []AliasMail `json:"messages"`
	} `json:"data"`
}

func decodePublicJSON(t *testing.T, raw string) publicMailJSON {
	t.Helper()
	var out publicMailJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, raw)
	}
	return out
}

// TestPublicMailRejectsUnknownToken 未知令牌、邮箱与令牌不匹配都必须回同一个 404。
func TestPublicMailRejectsUnknownToken(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	cases := []string{
		"/m?f=json&e=a@icloud.com:nosuchtoken", // 令牌不存在
		"/m?f=json&e=a@icloud.com",             // 没有令牌
		"/m?f=json",                            // 没有 e 参数
		"/m?f=json&e=nosuchalias",              // 格式不对
	}
	for _, path := range cases {
		code, raw, _ := do(t, mustGet(t, ts.URL+path))
		if code != http.StatusNotFound {
			t.Fatalf("%s: 期望 404, 实际 %d (%s)", path, code, raw)
		}
		if !strings.Contains(raw, "mailbox not found") {
			t.Fatalf("%s: 错误信息应为 mailbox not found, 实际 %s", path, raw)
		}
	}

	// 邮箱与令牌不匹配:拿 A 的令牌读 B 的邮箱
	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	code, raw, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=b@icloud.com:"+entry.Token))
	if code != http.StatusNotFound {
		t.Fatalf("邮箱与令牌不匹配应 404, 实际 %d (%s)", code, raw)
	}
	// 大小写不同的同一个邮箱应当放行(邮件地址不区分大小写)
	code, raw, _ = do(t, mustGet(t, ts.URL+"/m?f=json&e=A@iCloud.com:"+entry.Token))
	if code != http.StatusOK {
		t.Fatalf("同一邮箱的大小写变体应放行, 实际 %d (%s)", code, raw)
	}
}

// TestPublicMailJSONShape 校验成功响应的字段与"不泄漏账号信息"。
func TestPublicMailJSONShape(t *testing.T) {
	f := &fakeBackend{
		aliasMailResult: AliasMailResult{
			Recipient: "a@icloud.com",
			Method:    "imap",
			Messages: []AliasMail{
				{ID: "42", From: "noreply@example.com", To: "a@icloud.com", Subject: "验证码", Date: "2026-10-21T12:00:00+08:00", Preview: "123456", Body: "您的验证码是 123456"},
			},
		},
	}
	s, ts := newTestServer(f)
	defer ts.Close()

	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	code, raw, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=a@icloud.com:"+entry.Token))
	if code != http.StatusOK {
		t.Fatalf("期望 200, 实际 %d (%s)", code, raw)
	}
	out := decodePublicJSON(t, raw)
	if out.Code != 0 {
		t.Fatalf("code 应为 0, 实际 %d", out.Code)
	}
	if out.Data.Recipient != "a@icloud.com" {
		t.Fatalf("recipient 不对: %q", out.Data.Recipient)
	}
	if out.Data.MailQueryHours != 24 {
		t.Fatalf("默认时间窗应为 24 小时, 实际 %d", out.Data.MailQueryHours)
	}
	if out.Data.Count != 1 || len(out.Data.Messages) != 1 {
		t.Fatalf("应返回 1 封邮件, 实际 count=%d len=%d", out.Data.Count, len(out.Data.Messages))
	}
	if out.Data.Messages[0].Body == "" {
		t.Fatal("应带回正文(OTP 在正文里)")
	}
	// 账号信息绝不能出现在取件响应里
	for _, leak := range []string{"account_id", "acc_1", "anonymous", "method"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("响应不应包含 %q: %s", leak, raw)
		}
	}
}

// TestPublicMailWindowParam 校验 h / limit 的边界处理。
func TestPublicMailWindowParam(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()
	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	base := ts.URL + "/m?f=json&e=a@icloud.com:" + entry.Token

	// 正常值透传
	if code, raw, _ := do(t, mustGet(t, base+"&h=6")); code != http.StatusOK {
		t.Fatalf("h=6 应成功, 实际 %d (%s)", code, raw)
	}
	if f.aliasMailHours != 6 {
		t.Fatalf("h=6 应传给后端, 实际 %d", f.aliasMailHours)
	}
	// 越界值回落到默认 24
	if code, _, _ := do(t, mustGet(t, base+"&h=9999")); code != http.StatusOK {
		t.Fatalf("越界 h 不应报错")
	}
	if f.aliasMailHours != defaultPublicHours {
		t.Fatalf("越界 h 应回落默认值, 实际 %d", f.aliasMailHours)
	}
	// f 只接受 html / json
	code, raw, _ := do(t, mustGet(t, ts.URL+"/m?f=xml&e=a@icloud.com:"+entry.Token))
	if code != http.StatusBadRequest || !strings.Contains(raw, "f must be json or html") {
		t.Fatalf("非法 f 应 400, 实际 %d (%s)", code, raw)
	}
}

// TestPublicMailHTMLPage 校验 HTML 页面渲染与转义。
func TestPublicMailHTMLPage(t *testing.T) {
	f := &fakeBackend{
		aliasMailResult: AliasMailResult{
			Recipient: "a@icloud.com",
			Messages: []AliasMail{
				{
					ID: "1", From: "attacker@example.com", To: "a@icloud.com",
					Subject: `<script>alert(1)</script>`,
					Date:    "2026-10-21T12:00:00+08:00",
					Body:    `<img src=x onerror=alert(2)>`,
				},
			},
		},
	}
	s, ts := newTestServer(f)
	defer ts.Close()
	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}

	req := mustGet(t, ts.URL+"/m?e=a@icloud.com:"+entry.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("默认 f 应为 html 且 200, 实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 应为 html, 实际 %q", ct)
	}
	// 取件页放宽了 style-src,但脚本必须完全禁止
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP 应禁止脚本, 实际 %q", csp)
	}
	if tag := resp.Header.Get("X-Robots-Tag"); !strings.Contains(tag, "noindex") {
		t.Fatalf("带令牌的页面不应被搜索引擎收录, 实际 %q", tag)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("邮件主题必须被转义,不能原样输出")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Fatalf("应输出转义后的主题: %s", page)
	}
	if strings.Contains(page, "<img") {
		t.Fatal("正文必须被转义,不能输出可执行的标签")
	}
	if !strings.Contains(page, "&lt;img") {
		t.Fatalf("正文应作为文本转义输出: %s", page)
	}
	if !strings.Contains(page, "a@icloud.com") {
		t.Fatal("页面应显示收件别名")
	}
}

// TestIssueTokenAPI 覆盖签发接口:返回分发串、幂等、轮换。
func TestIssueTokenAPI(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	code, raw := adminDo(t, ts, session, csrf, "POST", "/api/alias-token",
		`{"account_id":"acc_1","email":"a@icloud.com","recipient":"客户甲"}`)
	if code != http.StatusOK {
		t.Fatalf("签发应成功, 实际 %d (%s)", code, raw)
	}
	var out struct {
		Data struct {
			Token      string `json:"token"`
			URL        string `json:"url"`
			Distribute string `json:"distribute"`
			State      string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" || out.Data.State != "issued" {
		t.Fatalf("应返回令牌并置为 issued: %s", raw)
	}
	if !strings.HasPrefix(out.Data.URL, "http") || !strings.Contains(out.Data.URL, "/m?f=html&e=a@icloud.com:") {
		t.Fatalf("取件链接格式不对: %q", out.Data.URL)
	}
	// 分发串格式:邮箱----链接
	if out.Data.Distribute != "a@icloud.com----"+out.Data.URL {
		t.Fatalf("分发串格式应为 邮箱----链接, 实际 %q", out.Data.Distribute)
	}

	// 幂等:再签一次拿到同一个令牌
	_, raw2 := adminDo(t, ts, session, csrf, "POST", "/api/alias-token",
		`{"account_id":"acc_1","email":"a@icloud.com"}`)
	if decodeToken(t, raw2) != out.Data.Token {
		t.Fatal("重复签发应返回原令牌")
	}
	// rotate:换新令牌
	_, raw3 := adminDo(t, ts, session, csrf, "POST", "/api/alias-token",
		`{"account_id":"acc_1","email":"a@icloud.com","rotate":true}`)
	if decodeToken(t, raw3) == out.Data.Token {
		t.Fatal("rotate=true 应换新令牌")
	}

	// 账号不存在时拒绝
	code, _ = adminDo(t, ts, session, csrf, "POST", "/api/alias-token",
		`{"account_id":"acc_missing","email":"b@icloud.com"}`)
	if code != http.StatusNotFound {
		t.Fatalf("账号不存在应 404, 实际 %d", code)
	}
}

func decodeToken(t *testing.T, raw string) string {
	t.Helper()
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, raw)
	}
	return out.Data.Token
}

// TestRevokeTokenAPI 撤销后取件链接必须立刻失效。
func TestRevokeTokenAPI(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	s, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=a@icloud.com:"+entry.Token)); code != http.StatusOK {
		t.Fatalf("撤销前应可读")
	}

	code, raw := adminDo(t, ts, session, csrf, "DELETE", "/api/alias-token?email=a@icloud.com", "")
	if code != http.StatusOK {
		t.Fatalf("撤销应成功, 实际 %d (%s)", code, raw)
	}
	// 撤销后令牌作废,但系统认得"这个链接曾经有效":返回 410 Gone,
	// 让对方看到"链接已失效"而不是以为地址打错了。
	if code, _, body := do(t, mustGet(t, ts.URL+"/m?f=json&e=a@icloud.com:"+entry.Token)); code != http.StatusGone {
		t.Fatalf("撤销后应 410, 实际 %d (%s)", code, body)
	}
}

// TestBulkIssueTokens 批量签发返回每条的分发链接。
func TestBulkIssueTokens(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1"}}}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	code, raw := adminDo(t, ts, session, csrf, "POST", "/api/alias-bulk",
		`{"account_id":"acc_1","action":"issue_token","aliases":[{"anonymous_id":"a1","email":"a@icloud.com"},{"anonymous_id":"a2","email":"b@icloud.com"}]}`)
	if code != http.StatusOK {
		t.Fatalf("批量签发应成功, 实际 %d (%s)", code, raw)
	}
	var out struct {
		Data struct {
			Succeeded int               `json:"succeeded"`
			Links     map[string]string `json:"links"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Succeeded != 2 || len(out.Data.Links) != 2 {
		t.Fatalf("应签发 2 条, 实际 %+v", out.Data)
	}
	if !strings.Contains(out.Data.Links["a@icloud.com"], "e=a@icloud.com:") {
		t.Fatalf("链接内容不对: %v", out.Data.Links)
	}
}

// TestPublicMailRateLimited 同一令牌高频请求会被限流。
func TestPublicMailRateLimited(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()
	entry, err := s.meta.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/m?f=json&e=a@icloud.com:" + entry.Token

	limited := 0
	for i := 0; i < 70; i++ {
		code, _, _ := do(t, mustGet(t, url))
		if code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("超过每分钟配额后应出现 429")
	}
}

func mustGet(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
