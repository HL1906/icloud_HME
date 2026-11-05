package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
	"icloud-hme/internal/mail"
)

// fakeBackend 是测试用内存 Backend,记录调用,不访问网络。
type fakeBackend struct {
	// bulk 操作会并发调用本类型,记录字段需要加锁
	mu sync.Mutex

	accounts []account.Summary
	aliases  []hme.Alias
	inbox    InboxResult
	created  *hme.CreateResult

	addedInput   account.AddAccountInput
	updatedID    string
	updatedInput account.UpdateAccountInput
	proxyID      string
	proxyValue   string
	cookiesID    string
	cookiesValue string
	appPwdID     string
	appPwdEmail  string
	loginID      string
	loginErr     error
	removedID    string
	removedOK    bool
	removeErr    error

	aliasActID     string
	aliasActActive bool
	aliasActErr    error
	aliasDeleteID  string
	aliasDeleteErr error
	// aliasFailIDs 让指定 anonymousID 的别名操作失败,用于批量部分失败用例
	aliasFailIDs   map[string]error
	listInboxQuery InboxQuery
	reloadCount    int

	gotMessageID     string
	deletedMessageID string
	deleteMessageErr error

	forwardQuery   ForwardQuery
	forwardResult  ForwardResult
	forwardErr     error
	ackedAccountID string
	ackedAlias     string
	ackedIDs       []string
	ackedForwarded bool
	ackErr         error

	// 收信活动扫描
	activity      AliasActivityScan
	activityErr   error
	activityDays  int
	activityLimit int
	activityCalls int

	// 按别名取信(对外取件接口)
	aliasMailResult    AliasMailResult
	aliasMailErr       error
	aliasMailAccountID string
	aliasMailAlias     string
	aliasMailHours     int
	aliasMailWithBody  bool
}

func (f *fakeBackend) ListAccounts() []account.Summary { return f.accounts }

func (f *fakeBackend) AddAccount(in account.AddAccountInput) (account.Summary, error) {
	f.addedInput = in
	return account.Summary{ID: "acc_new", Name: in.Name, Status: "pending"}, nil
}

func (f *fakeBackend) UpdateAccount(id string, in account.UpdateAccountInput) (account.Summary, error) {
	f.updatedID, f.updatedInput = id, in
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: 更新失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) UpdateProxy(id, proxy string) (account.Summary, error) {
	f.proxyID, f.proxyValue = id, proxy
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: 代理更新失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) UpdateCookies(id, cookies string) (account.Summary, error) {
	f.cookiesID, f.cookiesValue = id, cookies
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: cookie 更新失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) SetAppPassword(id, email, appPassword string) (account.Summary, error) {
	f.appPwdID, f.appPwdEmail = id, email
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: 密码设置失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) SetMailbox(id string, config account.MailboxConfig) (account.Summary, error) {
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: 收件邮箱设置失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) LoginAccount(id, password, otp string) (account.Summary, error) {
	f.loginID = id
	if f.loginErr != nil {
		return account.Summary{}, f.loginErr
	}
	if len(f.accounts) == 0 {
		return account.Summary{}, fmt.Errorf("fake: 登录失败")
	}
	return f.accounts[0], nil
}

func (f *fakeBackend) RemoveAccount(id string) (bool, error) {
	f.removedID = id
	return f.removedOK, f.removeErr
}

func (f *fakeBackend) CreateAlias(accountID, label string) (*hme.CreateResult, error) {
	return f.created, nil
}

func (f *fakeBackend) ListAliases(accountID string) ([]hme.Alias, error) {
	return f.aliases, nil
}

func (f *fakeBackend) SetAliasActive(accountID, anonymousID string, active bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliasActID, f.aliasActActive = anonymousID, active
	if err := f.aliasFailIDs[anonymousID]; err != nil {
		return false, err
	}
	return true, f.aliasActErr
}

func (f *fakeBackend) DeleteAlias(accountID, anonymousID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliasDeleteID = anonymousID
	if err := f.aliasFailIDs[anonymousID]; err != nil {
		return err
	}
	return f.aliasDeleteErr
}

// ScanAliasActivity 返回预置的扫描结果,并记录调用参数。
func (f *fakeBackend) ScanAliasActivity(accountID string, days, limit int) (AliasActivityScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activityCalls++
	f.activityDays, f.activityLimit = days, limit
	if f.activityErr != nil {
		return AliasActivityScan{}, f.activityErr
	}
	out := f.activity
	out.AccountID = accountID
	out.WindowDays = days
	if out.Method == "" {
		out.Method = "imap"
	}
	if out.Hits == nil {
		out.Hits = map[string]time.Time{}
	}
	return out, nil
}

func (f *fakeBackend) ListInbox(q InboxQuery) (InboxResult, error) {
	f.listInboxQuery = q
	return f.inbox, nil
}

func (f *fakeBackend) GetMessage(accountID, messageID string) (*mail.FullMessage, error) {
	f.gotMessageID = messageID
	return &mail.FullMessage{Message: mail.Message{ID: messageID}}, nil
}

func (f *fakeBackend) DeleteMessage(accountID, messageID string) error {
	f.deletedMessageID = messageID
	return f.deleteMessageErr
}

func (f *fakeBackend) FetchForForward(q ForwardQuery) (ForwardResult, error) {
	f.forwardQuery = q
	if f.forwardErr != nil {
		return ForwardResult{}, f.forwardErr
	}
	return f.forwardResult, nil
}

func (f *fakeBackend) MarkForwarded(accountID, alias string, ids []string, forwarded bool) (int, error) {
	f.ackedAccountID, f.ackedAlias, f.ackedIDs, f.ackedForwarded = accountID, alias, ids, forwarded
	return len(ids), f.ackErr
}

func (f *fakeBackend) FetchAliasMails(accountID, alias string, hours, limit int, withBody bool) (AliasMailResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliasMailAccountID = accountID
	f.aliasMailAlias = alias
	f.aliasMailHours = hours
	f.aliasMailWithBody = withBody
	if f.aliasMailErr != nil {
		return AliasMailResult{}, f.aliasMailErr
	}
	return f.aliasMailResult, nil
}

func (f *fakeBackend) Reload() error {
	f.reloadCount++
	return nil
}

// newTestServer 构造带固定密码与 fake backend 的测试 Server。
func newTestServer(f *fakeBackend) (*Server, *httptest.Server) {
	cfg := Config{
		Debug:         false,
		AdminPassword: "admin-pass-2026-strong",
		SessionTTL:    12 * time.Hour,
	}
	s, err := newWithBackend(f, cfg)
	if err != nil {
		// 固定密码合法,走到这里说明测试配置有误
		panic(fmt.Sprintf("newWithBackend: %v", err))
	}
	ts := httptest.NewServer(s.Handler())
	return s, ts
}

// mustServer 构造 Server,失败即终止测试(供各测试文件复用)。
func mustServer(t *testing.T, be Backend, cfg Config) *Server {
	t.Helper()
	s, err := newWithBackend(be, cfg)
	if err != nil {
		t.Fatalf("newWithBackend: %v", err)
	}
	return s
}

// login 登录测试服务并返回 session Cookie 与 CSRF。
func login(t *testing.T, ts *httptest.Server, password string) (sessionCookie, csrf string) {
	t.Helper()
	body := fmt.Sprintf(`{"password":%q}`, password)
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "hme_session" {
			return c.Value, out.Data.CSRFToken
		}
	}
	t.Fatalf("响应未设置 hme_session Cookie (status=%d)", resp.StatusCode)
	return "", ""
}

// authedReq 构造带会话 Cookie 与 CSRF 头的请求。
func authedReq(t *testing.T, ts *httptest.Server, method, path, body string) *http.Request {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func do(t *testing.T, req *http.Request) (int, string, []*http.Cookie) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw), resp.Cookies()
}
