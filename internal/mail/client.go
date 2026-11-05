// Package mail 实现 iCloud 邮件 IMAP 读取客户端。
//
// 通过 Apple 应用专用密码连接 imap.mail.me.com:993,
// 拉取隐私邮箱别名收到的邮件。对应原 Python 项目 icloud_mail.py。
package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/charset"
	"icloud-hme/internal/netsafe"
)

const (
	IMAPServer = "imap.mail.me.com"
	IMAPPort   = 993

	// previewFetchBytes 是列表查询时为生成预览而拉取的正文前缀字节数。
	// 只取前缀即可覆盖 OTP 验证码等关键信息,避免为 preview 下载整封正文。
	previewFetchBytes = 4096
	// previewMaxRunes 是返回给前端的预览最大字符数,防止超长正文撑爆响应。
	previewMaxRunes = 500
)

// Message 是一封邮件的摘要信息。
type Message struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Preview string `json:"preview"`
}

// FullMessage 是一封邮件的完整内容(含正文)。
type FullMessage struct {
	Message
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// Client 是 iCloud 邮件 IMAP 客户端。
type Client struct {
	username string
	password string
	server   string
	port     int
	cli      *client.Client
	// guard 非 nil 时,拨号前校验目标地址(防 SSRF)。
	guard *netsafe.Policy
}

// NewClient 创建 IMAP 客户端。需在调用其它方法前先 Connect。
func NewClient(appleID, appPassword string) *Client {
	return NewClientWithServer(appleID, appPassword, IMAPServer, IMAPPort)
}

// NewClientWithServer creates an IMAP client for a custom server.
func NewClientWithServer(username, password, server string, port int) *Client {
	return &Client{username: username, password: password, server: server, port: port}
}

// WithGuard 附加出站地址限制策略。用于用户自定义 IMAP 服务器的场景,
// 防止把本服务当作内网探测器(SSRF)。
func (c *Client) WithGuard(p netsafe.Policy) *Client {
	c.guard = &p
	return c
}

// imapDialer 把 netsafe.GuardedDialer 适配为 go-imap 的 Dialer 接口。
type imapDialer struct {
	d netsafe.GuardedDialer
}

// Dial 在建立连接前校验目标地址。
func (x *imapDialer) Dial(network, addr string) (net.Conn, error) {
	return x.d.DialContext(context.Background(), network, addr)
}

// addr 返回规范化的 host:port(IPv6 会正确加方括号)。
func (c *Client) addr() string {
	return net.JoinHostPort(c.server, strconv.Itoa(c.port))
}

// Connect 连接并登录 IMAP 服务器。已连接且存活时直接复用。
func (c *Client) Connect() error {
	if c.cli != nil {
		if err := c.cli.Noop(); err == nil {
			return nil
		}
		c.forceClose()
	}
	addr := c.addr()

	var cli *client.Client
	var err error
	if c.guard != nil {
		// 拨号层校验:在真正建连前拦截内网/保留地址
		dialer := &imapDialer{d: netsafe.GuardedDialer{Policy: *c.guard}}
		cli, err = client.DialWithDialerTLS(dialer, addr, nil)
	} else {
		cli, err = client.DialTLS(addr, nil)
	}
	if err != nil {
		return fmt.Errorf("IMAP 连接失败: %w", err)
	}
	if err := cli.Login(c.username, c.password); err != nil {
		_ = cli.Logout()
		return fmt.Errorf("IMAP 登录失败 — 请检查邮箱账号、授权码和服务器地址: %w", err)
	}
	c.cli = cli
	return nil
}

// Ping 探测连接是否仍可用(NOOP)。
func (c *Client) Ping() error {
	if c.cli == nil {
		return fmt.Errorf("未连接")
	}
	return c.cli.Noop()
}

// Disconnect 登出并关闭连接。
func (c *Client) Disconnect() {
	if c.cli != nil {
		_ = c.cli.Logout()
		c.cli = nil
	}
}

// forceClose 不发 LOGOUT, 直接掐断(坏连接/池丢弃时用)。
func (c *Client) forceClose() {
	if c.cli != nil {
		_ = c.cli.Terminate()
		c.cli = nil
	}
}

// InboxCount 返回收件箱邮件总数。
func (c *Client) InboxCount() (int, error) {
	if c.cli == nil {
		return 0, fmt.Errorf("未连接")
	}
	mbox, err := c.cli.Select("INBOX", false)
	if err != nil {
		return 0, err
	}
	return int(mbox.Messages), nil
}

// ListInbox 拉取收件箱最近 limit 封邮件摘要。
//
// days 用于过滤只看近 N 天的邮件(0 表示不限制)。
// 返回按时间倒序排列。
func (c *Client) ListInbox(limit int, days int) ([]Message, error) {
	if c.cli == nil {
		return nil, fmt.Errorf("未连接")
	}
	if limit <= 0 {
		limit = 50
	}

	mbox, err := c.cli.Select("INBOX", true)
	if err != nil {
		return nil, err
	}
	total := int(mbox.Messages)
	if total == 0 {
		return []Message{}, nil
	}

	// 计算起始序号(只取最近 limit 封)
	from := uint32(1)
	if uint32(limit) < mbox.Messages {
		from = mbox.Messages - uint32(limit) + 1
	}

	seqset := new(imap.SeqSet)
	seqset.AddRange(from, mbox.Messages)

	// 只拉信封 + 头部 + 正文前 N 字节用于生成预览,不再为 preview 下载整封正文。
	// 高频轮询(转发服务)场景下这是数量级的流量差异;需要完整正文时走 GetFull。
	section := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.TextSpecifier},
		Peek:         true,
		Partial:      []int{0, previewFetchBytes},
	}
	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchEnvelope,
		imap.FetchInternalDate,
		section.FetchItem(),
	}

	messages := make(chan *imap.Message, limit)
	done := make(chan error, 1)
	go func() {
		done <- c.cli.Fetch(seqset, items, messages)
	}()

	var out []Message
	for msg := range messages {
		m := toMessageWithBody(msg)
		// days 过滤
		if days > 0 {
			if t := parseMessageDate(m.Date); !t.IsZero() {
				if time.Since(t) > time.Duration(days)*24*time.Hour {
					continue
				}
			}
		}
		out = append(out, m)
	}
	if err := <-done; err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}

// DeliveryHit 是一封邮件的投递信息(只取信封,不下载正文)。
type DeliveryHit struct {
	Addresses []string
	Date      time.Time
}

// maxScanLimit 是一次收信扫描最多拉取的信封数量,防止误传超大值把连接拖住。
const maxScanLimit = 2000

// ScanRecipients 拉取收件箱最近 limit 封邮件的收件地址与时间(新→旧)。
//
// 与 ListInbox 的区别:只取信封(FetchEnvelope),不下载任何正文,
// 几百封也只有几十 KB,适合统计"每个别名最近一次收信时间"。
// days 只保留近 N 天的邮件(0 表示不限制)。
func (c *Client) ScanRecipients(limit, days int) ([]DeliveryHit, error) {
	if c.cli == nil {
		return nil, fmt.Errorf("未连接")
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > maxScanLimit {
		limit = maxScanLimit
	}
	if _, err := c.cli.Select("INBOX", true); err != nil {
		return nil, err
	}

	criteria := imap.NewSearchCriteria()
	if days > 0 {
		criteria.Since = time.Now().AddDate(0, 0, -days)
	}
	uids, err := c.cli.UidSearch(criteria)
	if err != nil {
		// 有些服务器对 SINCE 搜索支持不佳,退化为按序号取最近 limit 封
		return c.scanRecentBySeq(limit, days)
	}
	if len(uids) == 0 {
		return []DeliveryHit{}, nil
	}
	uids = newestUIDs(uids, limit)

	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)
	return c.fetchEnvelopes(seqset, true, days)
}

// scanRecentBySeq 不做服务端搜索,直接按序号拉最近 limit 封信封。
func (c *Client) scanRecentBySeq(limit, days int) ([]DeliveryHit, error) {
	mbox, err := c.cli.Select("INBOX", true)
	if err != nil {
		return nil, err
	}
	total := int(mbox.Messages)
	if total == 0 {
		return []DeliveryHit{}, nil
	}
	from := 1
	if total > limit {
		from = total - limit + 1
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(uint32(from), uint32(total))
	return c.fetchEnvelopes(seqset, false, days)
}

// fetchEnvelopes 抓取一批邮件的信封并转换为投递信息。byUID 决定用 UID 还是序号。
func (c *Client) fetchEnvelopes(seqset *imap.SeqSet, byUID bool, days int) ([]DeliveryHit, error) {
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate}
	messages := make(chan *imap.Message, 256)
	done := make(chan error, 1)
	go func() {
		if byUID {
			done <- c.cli.UidFetch(seqset, items, messages)
			return
		}
		done <- c.cli.Fetch(seqset, items, messages)
	}()

	out := make([]DeliveryHit, 0, 64)
	for msg := range messages {
		if msg == nil || msg.Envelope == nil {
			continue
		}
		date := msg.Envelope.Date
		if date.IsZero() {
			date = msg.InternalDate
		}
		if days > 0 && !date.IsZero() && time.Since(date) > time.Duration(days)*24*time.Hour {
			continue
		}
		addrs := make([]string, 0, len(msg.Envelope.To))
		for _, a := range msg.Envelope.To {
			if a == nil {
				continue
			}
			if addr := a.Address(); addr != "" {
				addrs = append(addrs, addr)
			}
		}
		if len(addrs) == 0 {
			continue
		}
		out = append(out, DeliveryHit{Addresses: addrs, Date: date})
	}
	if err := <-done; err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}

// ParseMessageDate 解析邮件头部日期。Web API 回退路径复用它,避免重复实现。
func ParseMessageDate(raw string) time.Time {
	return parseMessageDate(raw)
}

// SplitAddresses 从 To 头文本解析出邮箱地址列表。
//
// IMAP 信封里已经是结构化地址,只有 Web API 返回的是原始头部文本,
// 需要同时容忍 "名字 <a@b>" 与不规范的分隔符。
func SplitAddresses(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if list, err := mail.ParseAddressList(raw); err == nil {
		out := make([]string, 0, len(list))
		for _, a := range list {
			if a != nil && a.Address != "" {
				out = append(out, a.Address)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if i, j := strings.LastIndex(p, "<"), strings.LastIndex(p, ">"); i >= 0 && j > i {
			p = p[i+1 : j]
		}
		p = strings.TrimSpace(strings.Trim(p, `"`))
		if strings.Contains(p, "@") {
			out = append(out, p)
		}
	}
	return out
}

// FindByRecipient 查找发给指定隐私邮箱别名的最近 limit 封邮件(新→旧)。
//
// 先尝试 IMAP TO 搜索; 失败则只扫收件箱最近若干封本地过滤。
func (c *Client) FindByRecipient(recipient string, limit int, days int) ([]Message, error) {
	var out []Message
	err := c.ForEachByRecipient(recipient, limit, days, func(m Message) bool {
		out = append(out, m)
		return true // 收满 limit 为止
	})
	return out, err
}

// ForEachByRecipient 按新→旧遍历发给 recipient 的最近 limit 封邮件。
// onMsg 返回 false 时立即停止(用于 OTP 命中即返回)。
func (c *Client) ForEachByRecipient(recipient string, limit int, days int, onMsg func(Message) bool) error {
	if c.cli == nil {
		return fmt.Errorf("未连接")
	}
	if onMsg == nil {
		return fmt.Errorf("onMsg 不能为空")
	}
	if limit <= 0 {
		limit = 5
	}

	if _, err := c.cli.Select("INBOX", true); err != nil {
		return err
	}

	// 1) 服务端按 To 搜索
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("To", recipient)
	if days > 0 {
		criteria.Since = time.Now().AddDate(0, 0, -days)
	}
	uids, err := c.cli.UidSearch(criteria)
	if err == nil && len(uids) > 0 {
		// UID 升序 → 取最后 limit 个(最新) → 倒序遍历
		uids = newestUIDs(uids, limit)
		for i := len(uids) - 1; i >= 0; i-- {
			m, ferr := c.fetchOneUID(uids[i])
			if ferr != nil {
				return ferr
			}
			// 服务端 HEADER To 搜索是"包含"语义(不同服务器实现差异很大),
			// 这里用本地精确地址再校验一次:按别名分发取件链接时,
			// 一次误命中就等于把别人的邮件交出去。
			if !RecipientMatches(m.To, recipient) {
				continue
			}
			if !onMsg(m) {
				return nil
			}
		}
		return nil
	}

	// 2) fallback: 只扫最近 N 封信封, 命中 To 再拉 body
	return c.forEachRecentMatching(recipient, limit, days, onMsg)
}

// newestUIDs 保留 UID 列表中最新的 limit 个(假定 UID 升序)。
func newestUIDs(uids []uint32, limit int) []uint32 {
	if limit <= 0 || len(uids) <= limit {
		return uids
	}
	return uids[len(uids)-limit:]
}

// forEachRecentMatching 拉取收件箱最近 scan 封(仅 envelope), 本地按 To 过滤后再取 body。
func (c *Client) forEachRecentMatching(recipient string, limit int, days int, onMsg func(Message) bool) error {
	mbox, err := c.cli.Select("INBOX", true)
	if err != nil {
		return err
	}
	total := int(mbox.Messages)
	if total == 0 {
		return nil
	}
	// 只扫最近 scan 封, 避免全箱
	scan := limit * 4
	if scan < 20 {
		scan = 20
	}
	if scan > 80 {
		scan = 80
	}
	if scan > total {
		scan = total
	}
	from := mbox.Messages - uint32(scan) + 1
	seqset := new(imap.SeqSet)
	seqset.AddRange(from, mbox.Messages)

	// 仅 envelope + date, 不拉 body
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate}
	messages := make(chan *imap.Message, scan)
	done := make(chan error, 1)
	go func() {
		done <- c.cli.Fetch(seqset, items, messages)
	}()

	type cand struct {
		uid  uint32
		date time.Time
		to   string
	}
	var cands []cand
	for msg := range messages {
		if msg == nil || msg.Envelope == nil {
			continue
		}
		to := ""
		if len(msg.Envelope.To) > 0 {
			parts := make([]string, 0, len(msg.Envelope.To))
			for _, a := range msg.Envelope.To {
				parts = append(parts, a.Address())
			}
			to = strings.Join(parts, ", ")
		}
		// 精确地址匹配,不能用子串(见 RecipientMatches 的说明)
		if !RecipientMatches(to, recipient) {
			continue
		}
		if days > 0 && !msg.Envelope.Date.IsZero() {
			if time.Since(msg.Envelope.Date) > time.Duration(days)*24*time.Hour {
				continue
			}
		}
		cands = append(cands, cand{uid: msg.Uid, date: msg.Envelope.Date, to: to})
	}
	if err := <-done; err != nil {
		return err
	}
	// 新→旧
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].date.After(cands[i].date) {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	n := 0
	for _, cd := range cands {
		if n >= limit {
			break
		}
		m, ferr := c.fetchOneUID(cd.uid)
		if ferr != nil {
			return ferr
		}
		n++
		if !onMsg(m) {
			return nil
		}
	}
	return nil
}

// fetchOneUID 拉取单封邮件(含 body preview), 使用 BODY.PEEK 不标已读。
func (c *Client) fetchOneUID(uid uint32) (Message, error) {
	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate, section.FetchItem()}
	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.cli.UidFetch(seqset, items, messages)
	}()
	msg := <-messages
	if err := <-done; err != nil {
		return Message{}, err
	}
	if msg == nil {
		return Message{}, fmt.Errorf("邮件不存在 (uid=%d)", uid)
	}
	return toMessageWithBody(msg), nil
}

func (c *Client) fetchByUIDs(uids []uint32, limit int) ([]Message, error) {
	if len(uids) == 0 {
		return []Message{}, nil
	}
	uids = newestUIDs(uids, limit)
	var out []Message
	// 新→旧
	for i := len(uids) - 1; i >= 0; i-- {
		m, err := c.fetchOneUID(uids[i])
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// GetFull 获取单封邮件的完整内容(含正文)。
func (c *Client) GetFull(uid uint32) (*FullMessage, error) {
	if c.cli == nil {
		return nil, fmt.Errorf("未连接")
	}
	if _, err := c.cli.Select("INBOX", true); err != nil {
		return nil, err
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)

	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate, imap.FetchRFC822}
	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.cli.UidFetch(seqset, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("邮件不存在 (uid=%d)", uid)
	}

	full := &FullMessage{Message: toMessage(msg)}
	// 解析正文
	if r := msg.GetBody(&imap.BodySectionName{}); r != nil {
		if em, err := mail.ReadMessage(r); err == nil {
			body, _ := readBody(em)
			full.Body = body
			full.ContentType = em.Header.Get("Content-Type")
		}
	}
	return full, nil
}

// Delete 删除收件箱中指定 UID 的邮件。
func (c *Client) Delete(uid uint32) error {
	if c.cli == nil {
		return fmt.Errorf("未连接")
	}
	if uid == 0 {
		return fmt.Errorf("邮件 UID 无效")
	}
	if _, err := c.cli.Select("INBOX", false); err != nil {
		return err
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := c.cli.UidStore(seqset, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return c.cli.Expunge(nil)
}

// GetFullByID 按字符串 ID 取邮件详情。IMAP 的 ID 是 UID 的十进制字符串。
func (c *Client) GetFullByID(id string) (*FullMessage, error) {
	uid, err := ParseUID(id)
	if err != nil {
		return nil, err
	}
	return c.GetFull(uid)
}

// DeleteByID 按字符串 ID 删除邮件。IMAP 的 ID 是 UID 的十进制字符串。
func (c *Client) DeleteByID(id string) error {
	uid, err := ParseUID(id)
	if err != nil {
		return err
	}
	return c.Delete(uid)
}

// ParseUID 把字符串 ID 解析为 IMAP UID。非数字 ID 说明该邮件来自 Web API 模式。
func ParseUID(id string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(id), 10, 32)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("邮件 ID 无效: %q (IMAP 模式下应为 UID 数字)", id)
	}
	return uint32(v), nil
}

// ---- 解析工具 ----

// parseMessageDate 解析邮件日期字符串,兼容枚举的多种时间格式。
// 解析失败返回零值时间(调用方据此跳过过滤)。
func parseMessageDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"02 Jan 2006 15:04:05 -0700",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func toMessage(msg *imap.Message) Message {
	m := Message{}
	if msg.Uid > 0 {
		m.ID = fmt.Sprintf("%d", msg.Uid)
	}
	if msg.Envelope != nil {
		if len(msg.Envelope.From) > 0 {
			m.From = msg.Envelope.From[0].Address()
		}
		if len(msg.Envelope.To) > 0 {
			addrs := make([]string, 0, len(msg.Envelope.To))
			for _, a := range msg.Envelope.To {
				addrs = append(addrs, a.Address())
			}
			m.To = strings.Join(addrs, ", ")
		}
		m.Subject = decodeHeader(msg.Envelope.Subject)
		if !msg.Envelope.Date.IsZero() {
			m.Date = msg.Envelope.Date.Format(time.RFC3339)
		}
	}
	if m.From != "" {
		m.From = decodeHeader(m.From)
	}
	if m.To != "" {
		m.To = decodeHeader(m.To)
	}
	return m
}

// toMessageWithBody 在 toMessage 基础上解析正文填充 Preview(供 OTP 提取)。
//
// 只取正文前缀(FETCH BODY[TEXT]<0.N>),因此不能假设拿到的是完整 MIME 体:
// 解析失败时退化为直接清洗原始片段,保证预览仍然有内容。
func toMessageWithBody(msg *imap.Message) Message {
	m := toMessage(msg)
	// Fetch 可能用 BODY[] 或 BODY.PEEK[], 两种 section 都试
	for _, section := range []*imap.BodySectionName{
		{BodyPartName: imap.BodyPartName{Specifier: imap.TextSpecifier}, Peek: true, Partial: []int{0, previewFetchBytes}},
		{Peek: true},
		{},
	} {
		r := msg.GetBody(section)
		if r == nil {
			continue
		}
		raw, err := io.ReadAll(r)
		if err != nil || len(raw) == 0 {
			continue
		}
		preview := previewFromRawBody(raw)
		if preview != "" {
			m.Preview = preview
			break
		}
	}
	return m
}

// previewFromRawBody 从(可能被截断的)正文片段生成纯文本预览。
func previewFromRawBody(raw []byte) string {
	// 优先按 MIME 解析,拿到解码后的正文
	if em, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		if body, err := readBody(em); err == nil && strings.TrimSpace(body) != "" {
			return truncatePreview(body)
		}
	}
	// 片段不是完整 MIME(被截断/直接是正文),退化为原始文本清洗
	return truncatePreview(sanitizePreview(string(raw)))
}

// truncatePreview 限制预览长度(按字符,避免截断多字节字符)。
func truncatePreview(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= previewMaxRunes {
		return s
	}
	return strings.TrimSpace(string(runes[:previewMaxRunes])) + "…"
}

// decodeHeader 解码 RFC 2047 编码的邮件头(如 =?UTF-8?B?xxx?=)。
func decodeHeader(s string) string {
	if s == "" {
		return ""
	}
	dec := mime.WordDecoder{CharsetReader: charset.Reader}
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// readBody 读取邮件正文,优先 text/plain,其次从 HTML 提取纯文本。
//
// 邮件正文普遍是 multipart/alternative 且常带 base64 / quoted-printable
// 传输编码;旧实现只读顶层实体,会把 MIME 源码(boundary、编码块)当成正文,
// 导致预览与验证码提取失效。此处递归解析 MIME 树并解码传输编码。
func readBody(msg *mail.Message) (string, error) {
	text, isHTML, err := readEntity(textproto.MIMEHeader(msg.Header), msg.Body, 0)
	if err != nil {
		return "", err
	}
	if isHTML {
		return sanitizePreview(text), nil
	}
	return sanitizePlainPreview(text), nil
}

// maxMIMEDepth 限制 MIME 嵌套层数,防止恶意深度嵌套拖垮解析。
const maxMIMEDepth = 8

// maxBodyReadBytes 是单封邮件正文的读取上限(超出部分截断)。
const maxBodyReadBytes = 1 << 20 // 1 MiB

// readEntity 递归解析一个 MIME 实体,返回正文文本与它是否为 HTML。
//
// multipart/* 会遍历子部件并优先选择 text/plain;子部件损坏(常见于
// 只取了正文前 4KB 的预览场景)时返回已解析到的内容而不是整体失败。
func readEntity(header textproto.MIMEHeader, body io.Reader, depth int) (text string, isHTML bool, err error) {
	if depth > maxMIMEDepth {
		return "", false, fmt.Errorf("MIME 嵌套过深")
	}
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", false, fmt.Errorf("multipart 缺少 boundary")
		}
		mr := multipart.NewReader(io.LimitReader(body, maxBodyReadBytes), boundary)
		var plainText, htmlText string
		for {
			part, perr := mr.NextPart()
			if perr != nil {
				// io.EOF 是正常结束;其它错误说明数据被截断或损坏,
				// 用手上已有的内容降级返回。
				break
			}
			partText, partIsHTML, partErr := readEntity(part.Header, part, depth+1)
			_ = part.Close()
			if partErr != nil || strings.TrimSpace(partText) == "" {
				continue
			}
			if partIsHTML {
				if htmlText == "" {
					htmlText = partText
				}
			} else if plainText == "" {
				plainText = partText
			}
			if plainText != "" && htmlText != "" {
				break // 两种都拿到了,无需继续遍历
			}
		}
		if plainText != "" {
			return plainText, false, nil
		}
		if htmlText != "" {
			return htmlText, true, nil
		}
		return "", false, nil
	}

	// 叶子部件:附件类实体与转发的原始邮件不参与正文
	switch {
	case strings.HasPrefix(mediaType, "application/"),
		strings.HasPrefix(mediaType, "image/"),
		strings.HasPrefix(mediaType, "audio/"),
		strings.HasPrefix(mediaType, "video/"),
		mediaType == "message/rfc822":
		return "", false, nil
	}

	raw, readErr := io.ReadAll(io.LimitReader(body, maxBodyReadBytes))
	// 部件被截断(预览只取正文前缀)时仍可能有有效内容,
	// 只要读到了字节就不当作失败。
	if readErr != nil && len(raw) == 0 {
		return "", false, readErr
	}
	raw = decodeTransferEncoding(raw, header.Get("Content-Transfer-Encoding"))

	// 非 UTF-8 字符集(如 gb2312/gbk)按声明的 charset 转码
	if cs := strings.TrimSpace(params["charset"]); cs != "" &&
		!strings.EqualFold(cs, "utf-8") && !strings.EqualFold(cs, "us-ascii") {
		if r, cerr := charset.Reader(cs, bytes.NewReader(raw)); cerr == nil {
			if converted, rerr := io.ReadAll(io.LimitReader(r, maxBodyReadBytes)); rerr == nil {
				raw = converted
			}
		}
	}

	// 缺省 Content-Type 按纯文本处理(与旧行为一致)
	return string(raw), mediaType == "text/html", nil
}

// decodeTransferEncoding 解码 base64 / quoted-printable 传输编码。
// 解码失败(如预览取到的是被截断的编码流)时返回原始字节,交由上层降级处理。
func decodeTransferEncoding(raw []byte, cte string) []byte {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		cleaned := make([]byte, 0, len(raw))
		for _, b := range raw {
			switch b {
			case '\r', '\n', ' ', '\t':
				continue
			}
			cleaned = append(cleaned, b)
		}
		out := make([]byte, base64.StdEncoding.DecodedLen(len(cleaned)))
		if n, err := base64.StdEncoding.Decode(out, cleaned); err == nil {
			return out[:n]
		}
		// 去掉 padding 再试一次(部分邮件省略 '=')
		trimmed := bytes.TrimRight(cleaned, "=")
		out = make([]byte, base64.RawStdEncoding.DecodedLen(len(trimmed)))
		if n, err := base64.RawStdEncoding.Decode(out, trimmed); err == nil {
			return out[:n]
		}
		return raw
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return raw
		}
		return decoded
	}
	return raw
}
