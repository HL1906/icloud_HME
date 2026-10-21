// Package account 实现多账号管理器。
//
// 负责账号 CRUD、Cookie 解析(Header String / JSON)、持久化到 accounts.json,
// 以及创建 HME 客户端和邮件客户端。对应原 Python 项目 account_manager.py。
package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"icloud-hme/internal/fsutil"
	"icloud-hme/internal/hme"
	"icloud-hme/internal/mail"
	"icloud-hme/internal/netsafe"
)

// Account 描述一个 iCloud 账号。
type Account struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	RealEmail     string            `json:"real_email"`
	ICloudEmail   string            `json:"icloud_email"`
	Cookies       map[string]string `json:"cookies"`
	Host          string            `json:"host"`
	Proxy         string            `json:"proxy,omitempty"` // HTTP/SOCKS5 代理
	AppPassword   string            `json:"app_password,omitempty"`
	Mailbox       *MailboxConfig    `json:"mailbox,omitempty"`
	Status        string            `json:"status"` // active / error
	AliasTotal    int               `json:"alias_total"`
	AliasActive   int               `json:"alias_active"`
	LastValidated string            `json:"last_validated"`
	LastError     string            `json:"last_error,omitempty"`
	CreatedAt     string            `json:"created_at"`
}

// MailboxConfig describes an external mailbox used to receive forwarded mail.
type MailboxConfig struct {
	Provider string `json:"provider"`
	Email    string `json:"email"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	Password string `json:"password,omitempty"`
}

// maxConcurrentValidations 限制后台会话校验的并发度。
//
// 每次校验都要访问上游(最坏 3 次重试 × 30s),不限并发的话批量导入
// 会把出站连接和上游限流打满。
const maxConcurrentValidations = 4

// closeWait 是 Close 等待在途校验的最长时间。
// 校验卡在上游超时时不能让进程退不出去,超时后结果由 closed 丢弃。
const closeWait = 3 * time.Second

// Manager 管理多个 iCloud 账号,线程安全。
type Manager struct {
	mu       sync.RWMutex
	accounts map[string]*Account
	dataDir  string
	dataFile string
	imapPool *mail.Pool // IMAP 长连接池
	// mailboxGuard 是外部收件邮箱 IMAP 连通的地址限制(防 SSRF)。
	mailboxGuard netsafe.Policy

	// 后台会话校验:添加账号时不再同步等待上游(最坏 >90s 逼近写超时),
	// 改为先落盘返回,异步校验后回写 status。
	validateFn func(*Account)  // 校验实现,便于测试替换
	inflight   map[string]bool // 正在排队的账号 id,避免重复校验
	revalidate map[string]bool // 校验途中凭据又被替换,结束后需再跑一轮
	sem        chan struct{}   // 并发闸门
	wg         sync.WaitGroup  // 在途校验
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	closeOnce  sync.Once
}

// guardPolicy 返回外部邮箱的地址限制策略(值拷贝,线程安全)。
func (m *Manager) guardPolicy() netsafe.Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mailboxGuard
}

// cloneCookies 返回 Cookie map 的独立副本。
func cloneCookies(cookies map[string]string) map[string]string {
	if cookies == nil {
		return nil
	}
	cloned := make(map[string]string, len(cookies))
	for k, v := range cookies {
		cloned[k] = v
	}
	return cloned
}

// copyAccount 返回账号的深拷贝(含 Cookies map),必须在持锁时调用。
func copyAccount(acc *Account) *Account {
	if acc == nil {
		return nil
	}
	cp := *acc
	cp.Cookies = cloneCookies(acc.Cookies)
	return &cp
}

// NewManager 创建管理器。dataDir 用于存放 accounts.json。
func NewManager(dataDir string) (*Manager, error) {
	// 0700:目录内的 accounts.json 含 Cookie 与 App 专用密码,不应让其他本地用户进入
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		accounts:     make(map[string]*Account),
		dataDir:      dataDir,
		dataFile:     filepath.Join(dataDir, "accounts.json"),
		imapPool:     mail.NewPool(),
		mailboxGuard: netsafe.DefaultIMAPPolicy(),
		validateFn:   (*Account).validateCookies,
		inflight:     make(map[string]bool),
		revalidate:   make(map[string]bool),
		sem:          make(chan struct{}, maxConcurrentValidations),
		ctx:          ctx,
		cancel:       cancel,
	}
	if err := m.load(); err != nil {
		cancel()
		return nil, err
	}
	m.normalizeStaleStatus()
	return m, nil
}

// Close 释放 IMAP 连接池等资源,并等待在途的后台校验收尾。
//
// 后台校验可能正卡在上游超时里,这里不无限等:超时后直接放行退出,
// 迟到的校验结果会因 closed 标记被丢弃,不会污染已落盘的数据。
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.cancel()

		done := make(chan struct{})
		go func() {
			m.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(closeWait):
			log.Printf("[account] 后台校验未在 %s 内结束,直接关闭", closeWait)
		}

		if m.imapPool != nil {
			m.imapPool.Close()
		}
	})
}

// Reload 重新加载 accounts.json 配置文件。
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load()
}

func (m *Manager) load() error {
	raw, err := os.ReadFile(m.dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var wrapper struct {
		Accounts map[string]*Account `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}
	m.accounts = wrapper.Accounts
	if m.accounts == nil {
		m.accounts = make(map[string]*Account)
	}
	return nil
}

// normalizeStaleStatus 把启动时残留的 validating 状态回收为 pending。
//
// 进程在后台校验途中退出时,accounts.json 里会留下 validating;
// 该状态没有对应的 goroutine 会再推进它,不回收就会永久显示"校验中"。
func (m *Manager) normalizeStaleStatus() {
	for _, acc := range m.accounts {
		if acc != nil && acc.Status == "validating" {
			acc.Status = "pending"
			acc.LastError = ""
		}
	}
}

func (m *Manager) save() error {
	wrapper := struct {
		Accounts  map[string]*Account `json:"accounts"`
		UpdatedAt string              `json:"updated_at"`
	}{
		Accounts:  m.accounts,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入:避免写一半导致账号配置(含 Cookie)整体损坏
	return fsutil.WriteFileAtomic(m.dataFile, raw, 0o600)
}

// ParseCookieInput 解析 Cookie 输入,支持两种格式:
//   - Header String: "name1=value1; name2=value2; ..."
//   - JSON: {"name1":"value1","name2":"value2"}
//
// 空输入返回错误。
func ParseCookieInput(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("空白输入 — 请粘贴 Cookie Header String 或 JSON")
	}

	// JSON 格式
	if strings.HasPrefix(raw, "{") {
		var cookies map[string]string
		if err := json.Unmarshal([]byte(raw), &cookies); err == nil && cookies != nil {
			out := make(map[string]string, len(cookies))
			for k, v := range cookies {
				if v != "" {
					out[k] = v
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}

	// Header String 格式
	cookies := make(map[string]string)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])
		if name != "" {
			cookies[name] = value
		}
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("无法解析 Cookie 输入,请提供 Header String 或 JSON 格式")
	}
	return cookies, nil
}

// AddAccount 添加一个账号。cookieInput 可为空,后续可通过 /login 获取。
//
// cookieInput 支持 Header String 或 JSON。会话校验在后台进行:本方法只做
// 本地解析与落盘,不访问网络,调用方应通过账户 status(validating → active/error)
// 获取校验结果。
//
// 兼容入口:新调用方请使用 AddAccountWithInput。
func (m *Manager) AddAccount(name, cookieInput, host, proxy string) (*Account, error) {
	if host == "" {
		host = "icloud.com"
	}
	acc, err := m.newAccount(name, "", cookieInput, host, proxy)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.accounts[acc.ID] = acc
	saveErr := m.save()
	snap := copyAccount(acc)
	m.mu.Unlock()
	if saveErr != nil {
		return nil, saveErr
	}
	if len(snap.Cookies) > 0 {
		m.startValidation(acc.ID)
	}
	return snap, nil
}

// AddAccountWithInput 添加账号(带完整校验)。
//
// 输入校验(名称/邮箱/主机/代理/Cookie 格式)同步完成,失败立即报错;
// 需要访问 iCloud 的会话校验放到后台,避免请求线程被上游超时(最坏 >90s)
// 拖到超过 HTTP 写超时。返回的 Summary 中 status 为 validating 或 pending。
func (m *Manager) AddAccountWithInput(input AddAccountInput) (Summary, error) {
	name, err := validateName(input.Name)
	if err != nil {
		return Summary{}, err
	}
	if err := validateEmail(input.ICloudEmail); err != nil {
		return Summary{}, err
	}
	host, err := validateHost(input.Host)
	if err != nil {
		return Summary{}, err
	}
	proxy, err := validateProxy(input.Proxy)
	if err != nil {
		return Summary{}, err
	}
	acc, err := m.newAccount(name, input.ICloudEmail, input.CookieInput, host, proxy)
	if err != nil {
		return Summary{}, err
	}
	m.mu.Lock()
	m.accounts[acc.ID] = acc
	saveErr := m.save()
	// Summary 必须在锁内取:解锁后后台校验可能已经改写了 acc.Status
	sum := acc.Summary()
	hasCookies := len(acc.Cookies) > 0
	m.mu.Unlock()
	if saveErr != nil {
		return Summary{}, saveErr
	}
	if hasCookies {
		m.startValidation(acc.ID)
	}
	return sum, nil
}

// newAccount 构造账号,不访问网络。
//
// 有 Cookie 时状态为 validating,由调用方在落盘后调度后台校验。
func (m *Manager) newAccount(name, icloudEmail, cookieInput, host, proxy string) (*Account, error) {
	var cookies map[string]string
	if cookieInput != "" {
		var err error
		cookies, err = ParseCookieInput(cookieInput)
		if err != nil {
			return nil, err
		}
	} else {
		cookies = make(map[string]string)
	}

	status := "pending"
	if len(cookies) > 0 {
		status = "validating"
	}

	return &Account{
		ID:          "acc_" + uuid.New().String()[:8],
		Name:        name,
		RealEmail:   icloudEmail,
		ICloudEmail: icloudEmail,
		Cookies:     cookies,
		Host:        host,
		Proxy:       proxy,
		Status:      status,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}, nil
}

// startValidation 异步校验指定账号的会话。
//
// 同一账号同时只会有一个校验任务。若校验在途时凭据又被替换(用户改了 Cookie),
// 这里只记一个"再来一轮"标记,由当前任务结束后自己再跑一次——否则新凭据
// 会一直停在 validating(旧结果因 Cookie 不匹配被丢弃,又没人补跑)。
func (m *Manager) startValidation(id string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.inflight[id] {
		m.revalidate[id] = true
		m.mu.Unlock()
		return
	}
	m.inflight[id] = true
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		for {
			select {
			case m.sem <- struct{}{}:
			case <-m.ctx.Done():
				m.finishValidation(id)
				return
			}
			m.validateInBackground(id)
			<-m.sem

			m.mu.Lock()
			again := m.revalidate[id]
			delete(m.revalidate, id)
			_, exists := m.accounts[id]
			m.mu.Unlock()
			if !again || !exists || m.isClosed() {
				break
			}
		}
		m.finishValidation(id)
	}()
}

// finishValidation 清理账号的在途标记。
func (m *Manager) finishValidation(id string) {
	m.mu.Lock()
	delete(m.inflight, id)
	delete(m.revalidate, id)
	m.mu.Unlock()
}

// isClosed 返回管理器是否已关闭。
func (m *Manager) isClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

// validateInBackground 在锁外校验会话,再持锁回写状态。
func (m *Manager) validateInBackground(id string) {
	// 校验实现里的 panic 不能带崩整个进程
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[account] 后台校验 panic id=%s: %v", id, r)
		}
	}()

	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok || len(snap.Cookies) == 0 {
		return
	}

	origCookies := cloneCookies(snap.Cookies)
	m.validateFn(snap)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	cur, ok := m.accounts[id]
	if !ok {
		return
	}
	// 校验期间 Cookie 被改过:本次结果基于旧凭据,丢弃以免覆盖新值
	if !sameCookies(cur.Cookies, origCookies) {
		return
	}
	cur.Cookies = snap.Cookies
	cur.Status = snap.Status
	cur.LastError = snap.LastError
	cur.LastValidated = snap.LastValidated
	cur.RealEmail = snap.RealEmail
	cur.AliasTotal = snap.AliasTotal
	cur.AliasActive = snap.AliasActive
	if cur.ICloudEmail == "" {
		cur.ICloudEmail = snap.ICloudEmail
	}
	if err := m.save(); err != nil {
		log.Printf("[account] 后台校验结果落盘失败 id=%s: %v", id, err)
	}
}

// validateCookies 用 Cookie 校验会话并填充账号身份(在锁外对快照操作)。
func (a *Account) validateCookies() {
	host := a.Host
	if host == "" {
		host = "icloud.com"
	}
	client, err := hme.NewClient(a.Cookies, host, a.Proxy, false)
	if err != nil {
		a.Status = "error"
		a.LastError = truncate(err.Error(), 300)
		return
	}
	if err := client.ValidateSession(); err != nil {
		// validate 即使失败也可能通过 Set-Cookie 刷新部分会话状态。
		a.Cookies = client.CookieSnapshot()
		a.Status = "error"
		a.LastError = truncate(err.Error(), 300)
		return
	}
	// 显式接收 validate 刷新的 Cookie，不依赖传入 map 的引用关系。
	a.Cookies = client.CookieSnapshot()
	a.Status = "active"
	if info := client.AccountInfo(); info != nil {
		a.RealEmail = firstNonEmpty(info.AppleID, info.PrimaryEmail)
		if a.ICloudEmail == "" {
			a.ICloudEmail = deriveICloudEmail(info)
		}
	}
	if aliases, err := client.ListAliases(); err == nil {
		a.AliasTotal = len(aliases)
		for _, al := range aliases {
			if al.Active {
				a.AliasActive++
			}
		}
	}
	a.LastValidated = time.Now().Format(time.RFC3339)
}

// UpdateMetadata 编辑账号基本信息(名称、iCloud 邮箱、主机),至少提供一个字段。
func (m *Manager) UpdateMetadata(id string, input UpdateAccountInput) (Summary, error) {
	if input.Name == nil && input.ICloudEmail == nil && input.Host == nil {
		return Summary{}, fmt.Errorf("至少需要提供一个可编辑字段")
	}
	var name, email, host *string
	if input.Name != nil {
		v, err := validateName(*input.Name)
		if err != nil {
			return Summary{}, err
		}
		name = &v
	}
	if input.ICloudEmail != nil {
		if err := validateEmail(*input.ICloudEmail); err != nil {
			return Summary{}, err
		}
		v := strings.TrimSpace(*input.ICloudEmail)
		email = &v
	}
	if input.Host != nil {
		v, err := validateHost(*input.Host)
		if err != nil {
			return Summary{}, err
		}
		host = &v
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return Summary{}, fmt.Errorf("账号不存在: %s", id)
	}
	if name != nil {
		acc.Name = *name
	}
	if email != nil {
		acc.ICloudEmail = *email
	}
	if host != nil {
		acc.Host = *host
	}
	if err := m.save(); err != nil {
		return Summary{}, err
	}
	return acc.Summary(), nil
}

// UpdateProxy 更新或清除账号代理。空字符串表示清除。
func (m *Manager) UpdateProxy(id, proxy string) (Summary, error) {
	proxy, err := validateProxy(proxy)
	if err != nil {
		return Summary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return Summary{}, fmt.Errorf("账号不存在: %s", id)
	}
	acc.Proxy = proxy
	if err := m.save(); err != nil {
		return Summary{}, err
	}
	return acc.Summary(), nil
}

// RemoveAccount 删除账号。
//
// 落盘失败时回滚内存删除:否则"内存已删、盘上还在"会让账号在下次启动时复活。
func (m *Manager) RemoveAccount(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return false, nil
	}
	delete(m.accounts, id)
	if err := m.save(); err != nil {
		m.accounts[id] = acc
		return true, err
	}
	// 在途校验会在回写时发现账号已不存在而自动退出
	delete(m.revalidate, id)
	return true, nil
}

// GetAccount 返回账号深拷贝(含 Cookies),调用方可安全使用。
func (m *Manager) GetAccount(id string) (*Account, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	acc, ok := m.accounts[id]
	if !ok {
		return nil, false
	}
	return copyAccount(acc), true
}

// ListAccounts 返回所有账号的深拷贝(脱敏,不含 Cookies),按活跃状态排序。
// 兼容入口:新调用方请使用 ListSummaries。
func (m *Manager) ListAccounts() []*Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Account, 0, len(m.accounts))
	for _, acc := range m.accounts {
		cp := copyAccount(acc)
		cp.Cookies = nil
		cp.AppPassword = ""
		if acc.Mailbox != nil {
			mailbox := *acc.Mailbox
			mailbox.Password = ""
			cp.Mailbox = &mailbox
		}
		out = append(out, cp)
	}
	return out
}

// ListSummaries 返回所有账号的安全摘要,排序为 active → pending → error,
// 同状态按 name、id 升序。
func (m *Manager) ListSummaries() []Summary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Summary, 0, len(m.accounts))
	for _, acc := range m.accounts {
		out = append(out, acc.Summary())
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := statusRank(out[i].Status), statusRank(out[j].Status)
		if ri != rj {
			return ri < rj
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// statusRank 返回状态的排序权重。
func statusRank(status string) int {
	switch status {
	case "active":
		return 0
	case "validating":
		return 1
	case "pending":
		return 2
	default:
		return 3
	}
}

// HMEClient 为指定账号创建一个新的 HME 客户端。
// 必须有有效的 Cookie 才能使用 HME 功能。
func (m *Manager) HMEClient(id string, verbose bool) (*hme.Client, error) {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("账号不存在: %s", id)
	}
	if len(snap.Cookies) == 0 {
		return nil, fmt.Errorf("账号未配置 Cookie，无法使用 HME 功能")
	}
	return hme.NewClient(snap.Cookies, snap.Host, snap.Proxy, verbose)
}

// HMEClientWithPassword 为指定账号创建一个新的 HME 客户端,使用账号密码登录。
// 登录成功后会自动获取 Cookie 并保存到账号配置。
func (m *Manager) HMEClientWithPassword(id, password string, otpProvider hme.OTPProvider) (*hme.Client, error) {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("账号不存在: %s", id)
	}

	email := snap.ICloudEmail
	if email == "" {
		email = snap.RealEmail
	}
	if email == "" {
		return nil, fmt.Errorf("账号未设置邮箱地址")
	}

	client, err := hme.NewClient(nil, snap.Host, snap.Proxy, true)
	if err != nil {
		return nil, err
	}

	if err := client.Login(email, password, otpProvider); err != nil {
		return nil, err
	}

	// 先保存 accountLogin 返回的 Cookie，随后通过 validate 刷新会话并再次持久化。
	// 国区与美区都走同一条刷新链路，避免只保存登录阶段的临时 token。
	if err := m.SaveCookies(id, client.CookieSnapshot()); err != nil {
		return nil, err
	}
	if err := client.ValidateSession(); err != nil {
		// validate 的失败响应也可能携带 Set-Cookie，尽量保留服务端最新状态。
		_ = m.SaveCookies(id, client.CookieSnapshot())
		return nil, err
	}

	// 保存 validate 刷新后的 Cookie 和账号状态。
	m.mu.Lock()
	cur, ok := m.accounts[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("账号不存在: %s", id)
	}
	cur.Cookies = client.CookieSnapshot()
	cur.Status = "active"
	cur.LastValidated = time.Now().Format(time.RFC3339)
	cur.LastError = ""
	if info := client.AccountInfo(); info != nil {
		cur.RealEmail = firstNonEmpty(info.AppleID, info.PrimaryEmail)
		if cur.ICloudEmail == "" {
			cur.ICloudEmail = deriveICloudEmail(info)
		}
	}
	saveErr := m.save()
	m.mu.Unlock()
	if saveErr != nil {
		return nil, saveErr
	}

	return client, nil
}

// MailClient 为指定账号创建 IMAP 邮件客户端(每次新建, 不走连接池)。
// 需要事先设置 iCloud 邮箱和 App 专用密码。
// 高频读信请用 WithMailClient 复用长连接。
func (m *Manager) MailClient(id string) (*mail.Client, error) {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("账号不存在: %s", id)
	}
	if snap.Mailbox != nil && snap.Mailbox.Email != "" && snap.Mailbox.Password != "" {
		return mail.NewClientWithServer(snap.Mailbox.Email, snap.Mailbox.Password, snap.Mailbox.IMAPHost, snap.Mailbox.IMAPPort).
			WithGuard(m.guardPolicy()), nil
	}
	imapEmail := snap.ICloudEmail
	if imapEmail == "" {
		imapEmail = snap.RealEmail
	}
	if !isICloudDomain(imapEmail) {
		return nil, fmt.Errorf("账号未设置 iCloud 邮箱 (当前: %s)", imapEmail)
	}
	if snap.AppPassword == "" {
		return nil, fmt.Errorf("账号未设置 App 专用密码")
	}
	return mail.NewClient(imapEmail, snap.AppPassword), nil
}

// WithMailClient 使用连接池中的长连接执行 fn(串行/账号级)。
// fn 返回后连接保留在池中, 不会 Logout。
func (m *Manager) WithMailClient(id string, fn func(*mail.Client) error) error {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var mailbox *MailboxConfig
	if ok && acc.Mailbox != nil {
		copy := *acc.Mailbox
		mailbox = &copy
	}
	m.mu.RUnlock()
	if mailbox != nil && mailbox.Email != "" && mailbox.Password != "" {
		mc := mail.NewClientWithServer(mailbox.Email, mailbox.Password, mailbox.IMAPHost, mailbox.IMAPPort).
			WithGuard(m.guardPolicy())
		if err := mc.Connect(); err != nil {
			return err
		}
		defer mc.Disconnect()
		return fn(mc)
	}
	imapEmail, appPassword, err := m.imapCreds(id)
	if err != nil {
		return err
	}
	if m.imapPool == nil {
		m.imapPool = mail.NewPool()
	}
	return m.imapPool.Do(imapEmail, appPassword, fn)
}

func (m *Manager) imapCreds(id string) (imapEmail, appPassword string, err error) {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("账号不存在: %s", id)
	}
	imapEmail = snap.ICloudEmail
	if imapEmail == "" {
		imapEmail = snap.RealEmail
	}
	if !isICloudDomain(imapEmail) {
		return "", "", fmt.Errorf("账号未设置 iCloud 邮箱 (当前: %s)", imapEmail)
	}
	if snap.AppPassword == "" {
		return "", "", fmt.Errorf("账号未设置 App 专用密码")
	}
	return imapEmail, snap.AppPassword, nil
}

// SetMailbox validates and stores an external IMAP mailbox after testing it.
func (m *Manager) SetMailbox(id string, config MailboxConfig) error {
	config.Provider = strings.TrimSpace(config.Provider)
	config.Email = strings.TrimSpace(config.Email)
	config.IMAPHost = strings.TrimSpace(config.IMAPHost)
	if config.Email == "" || config.IMAPHost == "" || config.Password == "" {
		return fmt.Errorf("收件邮箱、IMAP 服务器和授权码不能为空")
	}
	if strings.Contains(config.IMAPHost, "://") || strings.ContainsAny(config.IMAPHost, "/?#@") {
		return fmt.Errorf("IMAP 服务器或端口无效")
	}
	if config.IMAPPort < 1 || config.IMAPPort > 65535 {
		return fmt.Errorf("IMAP 服务器或端口无效")
	}
	// SSRF 防护:外部主机必须是公网可达的常见 IMAP 端点,禁止指向内网/回环/保留地址
	if err := m.mailboxGuard.CheckAddr(config.IMAPHost, config.IMAPPort); err != nil {
		return fmt.Errorf("IMAP 服务器地址不被允许: %w", err)
	}
	m.mu.RLock()
	_, ok := m.accounts[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}
	mc := mail.NewClientWithServer(config.Email, config.Password, config.IMAPHost, config.IMAPPort).
		WithGuard(m.guardPolicy())
	if err := mc.Connect(); err != nil {
		return err
	}
	_, err := mc.InboxCount()
	mc.Disconnect()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}
	acc.Mailbox = &config
	return m.save()
}

// WebMailClient 为指定账号创建 Web 邮件客户端。
// 使用 Cookie 认证，无需 App Password。
func (m *Manager) WebMailClient(id string) (*mail.WebClient, error) {
	m.mu.RLock()
	acc, ok := m.accounts[id]
	var snap *Account
	if ok {
		snap = copyAccount(acc)
	}
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("账号不存在: %s", id)
	}
	if len(snap.Cookies) == 0 {
		return nil, fmt.Errorf("账号未配置 Cookie，无法读取邮件")
	}
	// 从 cookies 中获取 dsid
	dsid := ""
	if v, ok := snap.Cookies["X-APPLE-WEBAUTH-USER"]; ok {
		// 解析 "v=1:s=1:d=22789132008" 格式
		parts := strings.Split(v, ":d=")
		if len(parts) == 2 {
			dsid = parts[1]
		}
	}
	return mail.NewWebClient(snap.Cookies, dsid, snap.Host)
}

// SetAppPassword 设置 iCloud 邮箱和 App 专用密码,并测试 IMAP 连接。
func (m *Manager) SetAppPassword(id, icloudEmail, appPassword string) error {
	if icloudEmail == "" {
		return fmt.Errorf("iCloud 邮箱不能为空")
	}
	if appPassword == "" {
		return fmt.Errorf("App 专用密码不能为空")
	}

	m.mu.RLock()
	_, ok := m.accounts[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}

	// 测试连接(锁外)
	mc := mail.NewClient(icloudEmail, appPassword)
	if err := mc.Connect(); err != nil {
		return err
	}
	count, err := mc.InboxCount()
	mc.Disconnect()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}
	acc.ICloudEmail = icloudEmail
	acc.AppPassword = appPassword
	if err := m.save(); err != nil {
		return err
	}
	_ = count
	return nil
}

// SaveCookies 保存指定账号的最新 Cookie（HMEClient 操作后刷新的 token）。
// 用于客户端 validate/操作过程中从 Set-Cookie 获取了新 token 后持久化。
func (m *Manager) SaveCookies(id string, cookies map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}
	acc.Cookies = cloneCookies(cookies)
	return m.save()
}

// SaveCookiesIfChanged 仅在 Cookie 实际变化时落盘。
//
// accounts.json 含全部账号的 Cookie,每次全量重写代价不小;
// 转发服务按别名轮询时,每次 ListAliases 都重写一次会造成明显的 IO 放大。
// 上游没有刷新 token(绝大多数请求)时直接跳过写盘。
func (m *Manager) SaveCookiesIfChanged(id string, cookies map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return fmt.Errorf("账号不存在: %s", id)
	}
	if sameCookies(acc.Cookies, cookies) {
		return nil
	}
	acc.Cookies = cloneCookies(cookies)
	return m.save()
}

// sameCookies 判断两份 Cookie 是否完全一致(键集合与值都相同)。
func sameCookies(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// UpdateCookies 更新指定账号的 Cookie,并在后台校验会话有效性。
//
// 只做本地写入并立即返回,不等待上游:校验结果通过 status 反映
// (validating → active/error),避免粘贴 Cookie 保存时被上游超时卡住 90s。
func (m *Manager) UpdateCookies(id string, cookies map[string]string) error {
	if len(cookies) == 0 {
		return fmt.Errorf("cookies 不能为空")
	}
	m.mu.Lock()
	cur, ok := m.accounts[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("账号不存在: %s", id)
	}
	cur.Cookies = cloneCookies(cookies)
	cur.Status = "validating"
	cur.LastError = ""
	if cur.Host == "" {
		cur.Host = "icloud.com"
	}
	saveErr := m.save()
	m.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	m.startValidation(id)
	return nil
}

// ---- 辅助函数 ----

// deriveICloudEmail 从账号身份推导 iCloud 邮箱地址(用于 IMAP 登录)。
//
// 规则:
//  1. primaryEmail 是 @icloud.com/@me.com/@mac.com → 直接用
//  2. appleId 是上述域名 → 直接用
//  3. appleId 是第三方邮箱(如 @qq.com) → 取 local part 拼 @icloud.com
func deriveICloudEmail(info *hme.AccountInfo) string {
	primary := strings.TrimSpace(info.PrimaryEmail)
	appleID := strings.TrimSpace(info.AppleID)

	if isICloudDomain(primary) {
		return primary
	}
	if isICloudDomain(appleID) {
		return appleID
	}
	if strings.Contains(appleID, "@") {
		local := strings.SplitN(appleID, "@", 2)[0]
		return local + "@icloud.com"
	}
	return firstNonEmpty(primary, appleID)
}

func isICloudDomain(email string) bool {
	return email != "" && (strings.Contains(email, "@icloud.com") ||
		strings.Contains(email, "@me.com") ||
		strings.Contains(email, "@mac.com"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
