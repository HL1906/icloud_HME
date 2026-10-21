// Package server - 可替换业务接口与 Manager 适配器。
//
// Backend 边界固定为高层业务动作,不把具体 *hme.Client 或 *mail.Client 暴露给 handler。
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"icloud-hme/internal/account"
	"icloud-hme/internal/forward"
	"icloud-hme/internal/hme"
	"icloud-hme/internal/mail"
)

// BackendError 是后端返回的稳定错误,携带 HTTP 状态码与稳定错误码。
type BackendError struct {
	Status  int
	Code    string
	Message string
}

func (e *BackendError) Error() string { return e.Message }

// InboxQuery 是收件箱查询参数。
type InboxQuery struct {
	AccountID string
	Alias     string
	Limit     int
	Days      int
}

// InboxResult 是收件箱查询结果。
type InboxResult struct {
	AccountID string         `json:"account_id"`
	Alias     string         `json:"alias,omitempty"`
	Count     int            `json:"count"`
	Messages  []mail.Message `json:"messages"`
	Method    string         `json:"method"`
}

// ForwardQuery 是转发专用查询参数。
//
// 转发服务的典型用法:按别名周期轮询,只取"还没转发过"的新邮件,
// 拿到后调用 /api/forward/ack 打标记,从而避免重复转发。
type ForwardQuery struct {
	AccountID string // 必填
	Alias     string // 必填: 只取投递到该别名的邮件
	Limit     int
	Days      int
	// IncludeBody 为 true 时同时返回正文(省一次详情请求)
	IncludeBody bool
}

// ForwardMessage 是转发专用的单封邮件。
type ForwardMessage struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Date     string `json:"date"`
	Preview  string `json:"preview"`
	Body     string `json:"body,omitempty"`
	Forwarded bool  `json:"forwarded"`
}

// ForwardResult 是转发专用查询结果。
type ForwardResult struct {
	AccountID string           `json:"account_id"`
	Alias     string           `json:"alias"`
	Count     int              `json:"count"`
	Messages  []ForwardMessage `json:"messages"`
	Method    string           `json:"method"`
}

// Backend 是可替换的业务接口;handler 只依赖本接口,测试使用内存 fake。
type Backend interface {
	ListAccounts() []account.Summary
	AddAccount(account.AddAccountInput) (account.Summary, error)
	UpdateAccount(string, account.UpdateAccountInput) (account.Summary, error)
	UpdateProxy(string, string) (account.Summary, error)
	UpdateCookies(string, string) (account.Summary, error)
	SetAppPassword(string, string, string) (account.Summary, error)
	SetMailbox(string, account.MailboxConfig) (account.Summary, error)
	LoginAccount(string, string, string) (account.Summary, error)
	RemoveAccount(string) (bool, error)
	CreateAlias(string, string) (*hme.CreateResult, error)
	ListAliases(string) ([]hme.Alias, error)
	SetAliasActive(string, string, bool) (bool, error)
	DeleteAlias(string, string) error
	ListInbox(InboxQuery) (InboxResult, error)
	GetMessage(string, string) (*mail.FullMessage, error)
	DeleteMessage(string, string) error
	FetchForForward(ForwardQuery) (ForwardResult, error)
	MarkForwarded(string, string, []string, bool) (int, error)
	Reload() error
}

// managerBackend 是生产 Backend,包装 *account.Manager。
type managerBackend struct {
	mgr     *account.Manager
	forward *forward.Store // "已转发"标记,可为 nil(禁用去重)
	// demo 非 nil 时,别名接口改走内存假数据,不触碰真实 iCloud 账号。
	demo *demoStore

	// aliasCache 缓存别名列表,用于转发接口的归属校验,避免每次轮询都打上游。
	aliasMu    sync.Mutex
	aliasCache map[string]aliasCacheEntry
}

// aliasCacheEntry 是一条别名列表缓存。
type aliasCacheEntry struct {
	aliases []hme.Alias
	at      time.Time
}

// ListAccounts 返回账号安全摘要列表。
func (b *managerBackend) ListAccounts() []account.Summary {
	return b.mgr.ListSummaries()
}

// AddAccount 添加账号。
func (b *managerBackend) AddAccount(in account.AddAccountInput) (account.Summary, error) {
	sum, err := b.mgr.AddAccountWithInput(in)
	if err != nil {
		return account.Summary{}, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: err.Error()}
	}
	return sum, nil
}

// UpdateAccount 编辑账号基本信息。
func (b *managerBackend) UpdateAccount(id string, in account.UpdateAccountInput) (account.Summary, error) {
	sum, err := b.mgr.UpdateMetadata(id, in)
	if err != nil {
		return account.Summary{}, mapAccountErr(err)
	}
	return sum, nil
}

// UpdateProxy 更新或清除账号代理。
func (b *managerBackend) UpdateProxy(id, proxy string) (account.Summary, error) {
	sum, err := b.mgr.UpdateProxy(id, proxy)
	if err != nil {
		return account.Summary{}, mapAccountErr(err)
	}
	return sum, nil
}

// UpdateCookies 更新账号 Cookie。cookies 为原始文本(Header String 或 JSON)。
func (b *managerBackend) UpdateCookies(id, cookies string) (account.Summary, error) {
	parsed, err := account.ParseCookieInput(cookies)
	if err != nil {
		return account.Summary{}, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: err.Error()}
	}
	if err := b.mgr.UpdateCookies(id, parsed); err != nil {
		return account.Summary{}, mapAccountErr(err)
	}
	sum, ok := b.mgr.GetAccount(id)
	if !ok {
		return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	return sum.Summary(), nil
}

// SetAppPassword 设置 iCloud 邮箱与 App 专用密码并测试 IMAP 连接。
func (b *managerBackend) SetAppPassword(id, icloudEmail, appPassword string) (account.Summary, error) {
	if err := b.mgr.SetAppPassword(id, icloudEmail, appPassword); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "账号不存在") {
			return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
		}
		if strings.Contains(msg, "不能为空") {
			return account.Summary{}, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: msg}
		}
		// IMAP 连接失败属于上游错误,不拼接详细错误
		return account.Summary{}, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "IMAP 验证失败,请检查邮箱与 App 专用密码"}
	}
	sum, ok := b.mgr.GetAccount(id)
	if !ok {
		return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	return sum.Summary(), nil
}

// SetMailbox configures and verifies an external IMAP mailbox.
func (b *managerBackend) SetMailbox(id string, config account.MailboxConfig) (account.Summary, error) {
	if err := b.mgr.SetMailbox(id, config); err != nil {
		if strings.Contains(err.Error(), "账号不存在") {
			return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
		}
		return account.Summary{}, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "收件邮箱验证失败,请检查邮箱、授权码和 IMAP 配置"}
	}
	sum, ok := b.mgr.GetAccount(id)
	if !ok {
		return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	return sum.Summary(), nil
}

// LoginAccount 使用 iCloud 密码登录账号,成功只返回 Summary,绝不返回 Cookies。
func (b *managerBackend) LoginAccount(id, password, otpCode string) (account.Summary, error) {
	var otpProvider hme.OTPProvider
	if otpCode != "" {
		otp := otpCode
		otpProvider = func() (string, error) { return otp, nil }
	}

	client, err := b.mgr.HMEClientWithPassword(id, password, otpProvider)
	if err != nil {
		return account.Summary{}, classifyLoginErr(err)
	}
	_ = client
	sum, ok := b.mgr.GetAccount(id)
	if !ok {
		return account.Summary{}, &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	return sum.Summary(), nil
}

// classifyLoginErr 把 iCloud 登录错误映射为稳定错误。
func classifyLoginErr(err error) *BackendError {
	msg := err.Error()
	if strings.Contains(msg, "需要提供 OTP") {
		return &BackendError{Status: http.StatusConflict, Code: "OTP_REQUIRED", Message: "需要提供 OTP 验证码"}
	}
	if strings.Contains(msg, "2FA 验证失败") {
		return &BackendError{Status: http.StatusUnauthorized, Code: "OTP_INVALID", Message: "OTP 验证码错误"}
	}
	if strings.Contains(msg, "账号不存在") {
		return &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	if isSessionError(msg) {
		return &BackendError{Status: http.StatusUnauthorized, Code: "UPSTREAM_UNAUTHORIZED", Message: "iCloud 会话失效,请更新 Cookie"}
	}
	return &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "iCloud 登录失败,请稍后重试"}
}

// RemoveAccount 删除账号。
func (b *managerBackend) RemoveAccount(id string) (bool, error) {
	found, err := b.mgr.RemoveAccount(id)
	if err != nil {
		// 账号文件写不进去属于服务端故障,不能让用户以为删成功了
		return found, &BackendError{Status: http.StatusInternalServerError, Code: "STORAGE_FAILURE", Message: "账号删除未能保存,请稍后重试"}
	}
	return found, nil
}

// CreateAlias 创建 HME 别名。
func (b *managerBackend) CreateAlias(accountID, label string) (*hme.CreateResult, error) {
	// 演示模式:在内存里造一条,不消耗真实 iCloud 的别名额度
	if b.demo != nil {
		return b.demo.create(label), nil
	}
	client, err := b.mgr.HMEClient(accountID, false)
	if err != nil {
		return nil, mapAccountErr(err)
	}
	result, err := client.CreateAlias(label, 5)
	_ = b.mgr.SaveCookiesIfChanged(accountID, client.CookieSnapshot())
	b.invalidateAliasCache(accountID)
	if err != nil {
		return nil, classifyUpstreamErr("创建邮箱失败", err)
	}
	return result, nil
}

// ListAliases 列出账号的 HME 别名。
func (b *managerBackend) ListAliases(accountID string) ([]hme.Alias, error) {
	// 演示模式:账号没配凭据也能看到完整列表,便于确认字段与交互
	if b.demo != nil {
		return b.demo.list(), nil
	}
	client, err := b.mgr.HMEClient(accountID, false)
	if err != nil {
		return nil, mapAccountErr(err)
	}
	aliases, err := client.ListAliases()
	_ = b.mgr.SaveCookiesIfChanged(accountID, client.CookieSnapshot())
	if err != nil {
		return nil, classifyUpstreamErr("获取别名列表失败", err)
	}
	return aliases, nil
}

// SetAliasActive 停用或激活别名。
func (b *managerBackend) SetAliasActive(accountID, anonymousID string, active bool) (bool, error) {
	if b.demo != nil {
		return b.demo.setActive(anonymousID, active), nil
	}
	client, err := b.mgr.HMEClient(accountID, false)
	if err != nil {
		return false, mapAccountErr(err)
	}
	var success bool
	if active {
		success, err = client.ReactivateHME(anonymousID)
	} else {
		success, err = client.DeactivateHME(anonymousID)
	}
	_ = b.mgr.SaveCookiesIfChanged(accountID, client.CookieSnapshot())
	if err != nil {
		msg := "操作失败"
		if !active {
			msg = "停用失败"
		} else {
			msg = "激活失败"
		}
		return false, classifyUpstreamErr(msg, err)
	}
	return success, nil
}

// DeleteAlias 删除别名。
func (b *managerBackend) DeleteAlias(accountID, anonymousID string) error {
	if b.demo != nil {
		if !b.demo.remove(anonymousID) {
			return &BackendError{Status: http.StatusNotFound, Code: "ALIAS_NOT_FOUND", Message: "别名不存在"}
		}
		return nil
	}
	client, err := b.mgr.HMEClient(accountID, false)
	if err != nil {
		return mapAccountErr(err)
	}
	err = client.Delete(anonymousID)
	_ = b.mgr.SaveCookiesIfChanged(accountID, client.CookieSnapshot())
	b.invalidateAliasCache(accountID)
	if err != nil {
		return classifyUpstreamErr("删除失败", err)
	}
	return nil
}

// ListInbox 读取收件箱摘要:IMAP (App Password) 优先,Web API (Cookie) 回退。
func (b *managerBackend) ListInbox(q InboxQuery) (InboxResult, error) {
	// 优先使用 IMAP 连接池 (App Password 认证,复用长连接)
	var imapMessages []mail.Message
	poolErr := b.mgr.WithMailClient(q.AccountID, func(mc *mail.Client) error {
		var e error
		if q.Alias != "" {
			imapMessages, e = mc.FindByRecipient(q.Alias, q.Limit, q.Days)
		} else {
			imapMessages, e = mc.ListInbox(q.Limit, q.Days)
		}
		return e
	})
	if poolErr == nil {
		return InboxResult{
			AccountID: q.AccountID,
			Alias:     q.Alias,
			Count:     len(imapMessages),
			Messages:  imapMessages,
			Method:    "imap",
		}, nil
	}
	// IMAP 失败,继续尝试 Web API

	// 回退到 Web API (Cookie 认证,无需 App Password)
	wmc, err := b.mgr.WebMailClient(q.AccountID)
	if err != nil {
		return InboxResult{}, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "无可用邮件客户端: 需要 App Password 或 Cookie"}
	}

	if q.Alias != "" {
		messages, err := wmc.FindByAlias(q.Alias, q.Limit)
		if err != nil {
			return InboxResult{}, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件失败"}
		}
		return InboxResult{AccountID: q.AccountID, Alias: q.Alias, Count: len(messages), Messages: messages, Method: "web_api"}, nil
	}
	messages, err := wmc.ListInbox(q.Limit)
	if err != nil {
		return InboxResult{}, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件失败"}
	}
	return InboxResult{AccountID: q.AccountID, Count: len(messages), Messages: messages, Method: "web_api"}, nil
}

// GetMessage 读取单封邮件详情。
// IMAP 模式(ID 为数字 UID)走 IMAP;其余(Web API 的 threadId)走 Web API,
// 不再因为 ID 不是数字就直接 400。
func (b *managerBackend) GetMessage(accountID, messageID string) (*mail.FullMessage, error) {
	if _, err := mail.ParseUID(messageID); err == nil {
		mc, err := b.mgr.MailClient(accountID)
		if err != nil {
			return nil, mapAccountErr(err)
		}
		if err := mc.Connect(); err != nil {
			return nil, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件失败"}
		}
		defer mc.Disconnect()
		message, err := mc.GetFullByID(messageID)
		if err != nil {
			return nil, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件详情失败"}
		}
		return message, nil
	}

	// Web API 模式: threadId 形式的 ID
	wmc, err := b.mgr.WebMailClient(accountID)
	if err != nil {
		return nil, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "邮件 ID 格式无效: 当前账号未配置 Cookie,无法读取该邮件"}
	}
	message, err := wmc.GetFull(messageID)
	if err != nil {
		return nil, &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件详情失败"}
	}
	return message, nil
}

// DeleteMessage 删除单封邮件。Web API 不支持删除,只提示改用 Cookie/IMAP。
func (b *managerBackend) DeleteMessage(accountID, messageID string) error {
	if _, err := mail.ParseUID(messageID); err != nil {
		return &BackendError{Status: http.StatusBadRequest, Code: "UNSUPPORTED_IN_WEB_MODE", Message: "当前邮件来自 Web API 模式,不支持删除;请配置 App 专用密码后重试"}
	}
	mc, err := b.mgr.MailClient(accountID)
	if err != nil {
		return mapAccountErr(err)
	}
	if err := mc.Connect(); err != nil {
		return &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "删除邮件失败"}
	}
	defer mc.Disconnect()
	if err := mc.DeleteByID(messageID); err != nil {
		return &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "删除邮件失败"}
	}
	return nil
}

// FetchForForward 实现转发专用查询:按别名取邮件,并标注/过滤"已转发"状态。
//
// 与 ListInbox 的区别:
//   - 必须指定别名(转发服务按别名路由给不同人)
//   - 默认只返回尚未标记转发的邮件,天然避免重复转发
//   - 可选直接带回正文,省掉一次详情请求
func (b *managerBackend) FetchForForward(q ForwardQuery) (ForwardResult, error) {
	alias := strings.TrimSpace(q.Alias)
	if err := forward.ValidateIdentity(strings.TrimSpace(q.AccountID), alias); err != nil {
		return ForwardResult{}, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: err.Error()}
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	// 别名必须属于该账号:转发服务按别名把邮件交给不同的人,
	// 若不校验,"查询任意别名"就等于拿到了别人的 OTP。
	if err := b.assertAliasOwned(q.AccountID, alias); err != nil {
		return ForwardResult{}, err
	}

	sources, method, err := b.fetchAliasMessages(q)
	if err != nil {
		return ForwardResult{}, err
	}

	out := make([]ForwardMessage, 0, len(sources))
	for _, m := range sources {
		fm := ForwardMessage{
			ID:      m.ID,
			From:    m.From,
			To:      m.To,
			Subject: m.Subject,
			Date:    m.Date,
			Preview: m.Preview,
		}
		if b.forward != nil {
			fm.Forwarded = b.forward.IsForwarded(q.AccountID, alias, m.ID)
		}
		out = append(out, fm)
	}

	// 只保留尚未转发的邮件,天然避免重复转发
	filtered := out[:0]
	for _, fm := range out {
		if fm.Forwarded {
			continue
		}
		filtered = append(filtered, fm)
	}
	out = filtered
	if q.IncludeBody {
		for i := range out {
			if body, ok := b.fetchBody(q.AccountID, out[i].ID, method); ok {
				out[i].Body = body
			}
		}
	}

	return ForwardResult{
		AccountID: q.AccountID,
		Alias:     alias,
		Count:     len(out),
		Messages:  out,
		Method:    method,
	}, nil
}

// fetchAliasMessages 按别名取邮件,IMAP 优先、Web API 回退,返回实际使用的方式。
func (b *managerBackend) fetchAliasMessages(q ForwardQuery) ([]mail.Message, string, error) {
	var imapMessages []mail.Message
	poolErr := b.mgr.WithMailClient(q.AccountID, func(mc *mail.Client) error {
		var e error
		imapMessages, e = mc.FindByRecipient(q.Alias, q.Limit, q.Days)
		return e
	})
	if poolErr == nil {
		return imapMessages, "imap", nil
	}

	wmc, err := b.mgr.WebMailClient(q.AccountID)
	if err != nil {
		return nil, "", &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "无可用邮件客户端: 需要 App Password 或 Cookie"}
	}
	messages, err := wmc.FindByAlias(q.Alias, q.Limit)
	if err != nil {
		return nil, "", &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "读取邮件失败"}
	}
	return messages, "web_api", nil
}

// fetchBody 取单封邮件正文,失败时返回 ok=false(不阻断整批转发)。
func (b *managerBackend) fetchBody(accountID, messageID, method string) (string, bool) {
	if method == "web_api" {
		wmc, err := b.mgr.WebMailClient(accountID)
		if err != nil {
			return "", false
		}
		full, err := wmc.GetFull(messageID)
		if err != nil {
			return "", false
		}
		return full.Body, true
	}
	mc, err := b.mgr.MailClient(accountID)
	if err != nil {
		return "", false
	}
	if err := mc.Connect(); err != nil {
		return "", false
	}
	defer mc.Disconnect()
	full, err := mc.GetFullByID(messageID)
	if err != nil {
		return "", false
	}
	return full.Body, true
}

// assertAliasOwned 校验 alias 确实属于该账号。
//
// 实现上复用账号的别名列表(走 iCloud 接口,带短时缓存避免每次轮询都请求上游)。
func (b *managerBackend) assertAliasOwned(accountID, alias string) error {
	aliases, err := b.cachedAliases(accountID)
	if err != nil {
		// 无法取得别名列表时不放行,避免"取不到就跳过校验"
		return err
	}
	want := strings.ToLower(strings.TrimSpace(alias))
	for _, a := range aliases {
		if strings.ToLower(strings.TrimSpace(a.Email)) == want {
			return nil
		}
	}
	return &BackendError{Status: http.StatusForbidden, Code: "ALIAS_NOT_FOUND", Message: "该别名不属于当前账号"}
}

// aliasCacheTTL 是别名列表缓存时长。
const aliasCacheTTL = 60 * time.Second

// cachedAliases 返回账号的别名列表,带短时缓存以减少上游请求。
func (b *managerBackend) cachedAliases(accountID string) ([]hme.Alias, error) {
	b.aliasMu.Lock()
	if entry, ok := b.aliasCache[accountID]; ok && time.Since(entry.at) < aliasCacheTTL {
		b.aliasMu.Unlock()
		return entry.aliases, nil
	}
	// 顺手清掉已过期的条目:账号被删除后对应缓存不应长期驻留
	for id, entry := range b.aliasCache {
		if time.Since(entry.at) >= aliasCacheTTL {
			delete(b.aliasCache, id)
		}
	}
	b.aliasMu.Unlock()

	aliases, err := b.ListAliases(accountID)
	if err != nil {
		return nil, err
	}
	b.aliasMu.Lock()
	if b.aliasCache == nil {
		b.aliasCache = make(map[string]aliasCacheEntry)
	}
	b.aliasCache[accountID] = aliasCacheEntry{aliases: aliases, at: time.Now()}
	b.aliasMu.Unlock()
	return aliases, nil
}

// invalidateAliasCache 在别名变更后清掉缓存,避免用到过期数据。
func (b *managerBackend) invalidateAliasCache(accountID string) {
	b.aliasMu.Lock()
	defer b.aliasMu.Unlock()
	delete(b.aliasCache, accountID)
}

// MarkForwarded 批量标记邮件为已转发。
func (b *managerBackend) MarkForwarded(accountID, alias string, messageIDs []string, forwarded bool) (int, error) {
	if err := forward.ValidateIdentity(accountID, alias); err != nil {
		return 0, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: err.Error()}
	}
	if b.forward == nil {
		return 0, &BackendError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "转发标记存储不可用"}
	}
	// 与查询一致:只允许标记属于该账号的别名
	if err := b.assertAliasOwned(accountID, alias); err != nil {
		return 0, err
	}

	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "参数错误: ids 不能为空"}
	}

	// 一次性批量落盘:避免每个 id 都全量重写一次文件
	if forwarded {
		if err := b.forward.MarkAll(accountID, alias, ids); err != nil {
			return 0, &BackendError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "保存转发标记失败"}
		}
		return len(ids), nil
	}
	for _, id := range ids {
		if err := b.forward.Unmark(accountID, alias, id); err != nil {
			return 0, &BackendError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "保存转发标记失败"}
		}
	}
	return len(ids), nil
}

// Reload 重新加载配置。
func (b *managerBackend) Reload() error {
	if err := b.mgr.Reload(); err != nil {
		return &BackendError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "重新加载配置失败"}
	}
	return nil
}

// mapAccountErr 把账号管理器错误映射为稳定错误。
func mapAccountErr(err error) *BackendError {
	msg := err.Error()
	if strings.Contains(msg, "账号不存在") {
		return &BackendError{Status: http.StatusNotFound, Code: "ACCOUNT_NOT_FOUND", Message: "账号不存在"}
	}
	if strings.Contains(msg, "Cookie") && strings.Contains(msg, "未配置") {
		return &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "账号未配置 Cookie"}
	}
	return &BackendError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: msg}
}

// classifyUpstreamErr 把上游 (iCloud) 错误映射为稳定错误,不拼接上游响应体。
func classifyUpstreamErr(fixedMsg string, err error) *BackendError {
	if err == nil {
		return nil
	}
	if isSessionError(err.Error()) {
		return &BackendError{Status: http.StatusUnauthorized, Code: "UPSTREAM_UNAUTHORIZED", Message: "iCloud 会话失效,请更新 Cookie"}
	}
	return &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: fixedMsg}
}

// isSessionError 判断错误是否由会话失效引起。
func isSessionError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "401") || strings.Contains(m, "403") ||
		strings.Contains(m, "session") || strings.Contains(m, "cookie") ||
		strings.Contains(m, "unauthorized") || strings.Contains(m, "认证") ||
		strings.Contains(m, "会话校验失败")
}

// asBackendError 提取 BackendError,非 BackendError 统一为 INTERNAL_ERROR。
func asBackendError(err error) *BackendError {
	var be *BackendError
	if errors.As(err, &be) {
		return be
	}
	return &BackendError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "内部错误"}
}

// cookieInputToJSON 把 handler 解析出的 map 转回 JSON 文本,交给 ParseCookieInput。
func cookieInputToJSON(cookies map[string]string) string {
	raw, err := json.Marshal(cookies)
	if err != nil {
		return ""
	}
	return string(raw)
}
