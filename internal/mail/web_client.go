// Package mail - iCloud Web 邮件客户端
//
// 使用 Cookie 认证通过 iCloud Web API 读取邮件，
// 无需 App Password。基于 mccgateway 服务。
package mail

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

// WebClientBuildNumber 是与浏览器一致的 mccgateway 邮件接口构建号。
const WebClientBuildNumber = "2624Build13"

// WebClient 是 iCloud Web 邮件客户端。
type WebClient struct {
	cookies       map[string]string
	dsid          string
	clientID      string
	mccGatewayURL string
	host          string // "icloud.com" 或 "icloud.com.cn"
	httpc         tls_client.HttpClient
}

// NewWebClient 创建一个 Web 邮件客户端。
//
// 构造失败必须回传给调用方:忽略错误会得到一个 httpc 为 nil 的客户端,
// 后续请求会以空指针 panic 的形式失败,远比一个明确错误难排查。
func NewWebClient(cookies map[string]string, dsid, host string) (*WebClient, error) {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	}

	httpc, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("创建 Web 邮件客户端失败: %w", err)
	}

	if host == "" {
		host = "icloud.com"
	}

	c := &WebClient{
		cookies:  cookies,
		dsid:     dsid,
		clientID: uuid.New().String(),
		host:     host,
		httpc:    httpc,
	}

	// 设置 Cookie 到所有相关域名(确保跨域请求能传递 Cookie)
	if len(cookies) > 0 {
		suffix := "icloud.com"
		if host == "icloud.com.cn" {
			suffix = "icloud.com.cn"
		}
		domains := []string{
			"https://setup." + suffix,
			"https://www." + suffix,
			"https://p217-mccgateway." + suffix,
			"https://p217-maildomainws." + suffix,
		}
		for _, domain := range domains {
			u, _ := url.Parse(domain)
			httpCookies := make([]*http.Cookie, 0, len(cookies))
			for k, v := range cookies {
				httpCookies = append(httpCookies, &http.Cookie{
					Name:  k,
					Value: v,
					Path:  "/",
				})
			}
			jar.SetCookies(u, httpCookies)
		}
	}

	return c, nil
}

// origin 返回当前账号对应的 Web Origin。
func (c *WebClient) origin() string {
	return "https://www." + c.host
}

// setCommonHeaders 设置与浏览器一致的通用请求头。
func (c *WebClient) setCommonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin())
	req.Header.Set("Referer", c.origin()+"/")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
}

// withParams 给 URL 追加 clientBuildNumber / clientId / dsid 查询参数。
func (c *WebClient) withParams(rawURL string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%sclientBuildNumber=%s&clientMasteringNumber=%s&clientId=%s&dsid=%s",
		rawURL, sep, WebClientBuildNumber, WebClientBuildNumber, c.clientID, c.dsid)
}

// resolveMccGateway 从 validate 响应中获取 mccgateway URL。
func (c *WebClient) resolveMccGateway() error {
	if c.mccGatewayURL != "" {
		return nil
	}

	setupURL := "https://setup." + c.host + "/setup/ws/1/validate"
	req, err := http.NewRequest("POST", c.withParams(setupURL), nil)
	if err != nil {
		return err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("validate 失败: HTTP %d - %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Webservices struct {
			Mccgateway struct {
				URL string `json:"url"`
			} `json:"mccgateway"`
		} `json:"webservices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("解析 validate 响应失败: %w", err)
	}

	mccURL := parsed.Webservices.Mccgateway.URL
	if mccURL == "" {
		return fmt.Errorf("未找到 mccgateway URL,响应: %s", truncate(string(body), 200))
	}
	if !strings.HasPrefix(mccURL, "https://") {
		mccURL = "https://" + mccURL
	}
	// 去掉端口号(如 :443)——tls-client 的 cookie jar 按不带端口的 host 存储 Cookie,
	// 带端口的 URL 会导致 Cookie 无法附加,返回 403。
	if u, err := url.Parse(mccURL); err == nil && u.Host != "" {
		u.Host = u.Hostname()
		mccURL = u.String()
	}
	c.mccGatewayURL = strings.TrimRight(mccURL, "/")
	return nil
}

// threadSearchResp 是 thread/search 接口的响应结构。
type threadSearchResp struct {
	TotalThreadsReturned int `json:"totalThreadsReturned"`
	ThreadList           []struct {
		ThreadID  string   `json:"threadId"`
		Subject   string   `json:"subject"`
		Senders   []string `json:"senders"`
		Receivers []string `json:"receivers"` // 收件人(即投递到哪个 HME 别名),别名过滤依赖此字段
		Preview   string   `json:"preview"`
		Timestamp int64    `json:"timestamp"`
	} `json:"threadList"`
}

// search 执行 thread/search 请求,返回解析后的邮件列表。
func (c *WebClient) search(payload string) ([]Message, error) {
	if err := c.resolveMccGateway(); err != nil {
		return nil, err
	}

	searchURL := c.withParams(c.mccGatewayURL + "/mailws2/v1/thread/search")
	req, err := http.NewRequest("POST", searchURL, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取邮件失败: HTTP %d - %s", resp.StatusCode, truncate(string(body), 300))
	}
	if strings.Contains(string(body), `"success":false`) {
		return nil, fmt.Errorf("获取邮件失败: %s", truncate(string(body), 300))
	}

	var result threadSearchResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析邮件响应失败: %w", err)
	}

	messages := make([]Message, 0, len(result.ThreadList))
	for _, t := range result.ThreadList {
		from := ""
		if len(t.Senders) > 0 {
			from = t.Senders[0]
		}
		date := ""
		if t.Timestamp > 0 {
			date = time.UnixMilli(t.Timestamp).Format(time.RFC3339)
		}
		messages = append(messages, Message{
			ID:      t.ThreadID,
			From:    from,
			To:      strings.Join(t.Receivers, ", "),
			Subject: t.Subject,
			Preview: sanitizePreview(t.Preview),
			Date:    date,
		})
	}
	return messages, nil
}

// ListInbox 列出收件箱邮件。
func (c *WebClient) ListInbox(limit int) ([]Message, error) {
	payload := fmt.Sprintf(`{"responseType":"THREAD_DIGEST","includeFolderStatus":true,"maxResults":%d,"sessionHeaders":{"folder":"INBOX","modseq":null,"threadmodseq":null,"condstore":1,"qresync":1,"threadmode":1}}`, limit)
	return c.search(payload)
}

// SearchMails 搜索邮件。query 为空时等价于 ListInbox。
func (c *WebClient) SearchMails(query string, limit int) ([]Message, error) {
	if query == "" {
		return c.ListInbox(limit)
	}
	payload := fmt.Sprintf(`{"responseType":"THREAD_DIGEST","includeFolderStatus":false,"maxResults":%d,"query":%q,"sessionHeaders":{"folder":"INBOX","condstore":1,"qresync":1,"threadmode":1}}`, limit, query)
	return c.search(payload)
}

// FindByAlias 查找发给指定别名的邮件——在本地过滤(Web API 不支持收件人搜索)。
func (c *WebClient) FindByAlias(alias string, limit int) ([]Message, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return c.ListInbox(limit)
	}
	// 拉取收件箱全部邮件(最多取 2*limit),本地过滤
	batchSize := limit * 2
	if batchSize < 50 {
		batchSize = 50
	}
	raw, err := c.ListInbox(batchSize)
	if err != nil {
		return nil, err
	}

	// 本地过滤: 只认收件人(To)。
	// 注意: 不能拿 From/Subject 做匹配——那会把"发件人恰好是别名"或
	// "主题里提到别名"的无关邮件也算进来,导致转发服务串号。
	filtered := make([]Message, 0, limit)
	for _, m := range raw {
		if recipientMatches(m.To, alias) {
			filtered = append(filtered, m)
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered, nil
}

// recipientMatches 判断收件人字段中是否包含指定别名(大小写不敏感,按地址边界匹配)。
func recipientMatches(to, alias string) bool {
	to = strings.ToLower(to)
	if to == "" || alias == "" {
		return false
	}
	// 收件人可能是 "a@icloud.com, b@icloud.com",逐个比较避免子串误命中
	for _, part := range strings.Split(to, ",") {
		part = strings.TrimSpace(part)
		// 去掉 "显示名 <addr>" 形式的外壳
		if i := strings.LastIndex(part, "<"); i >= 0 {
			part = strings.TrimSuffix(strings.TrimSpace(part[i+1:]), ">")
		}
		if part == alias {
			return true
		}
	}
	return false
}

// messageBatchResp 是 message/batch 接口的响应结构(threadId → 具体邮件列表)。
type messageBatchResp struct {
	Messages []webMessage `json:"messages"`
}

// webMessage 是 Web API 返回的单封邮件。
type webMessage struct {
	ID        string `json:"id"`
	ThreadID  string `json:"threadId"`
	Subject   string `json:"subject"`
	Sender    string `json:"sender"`
	From      string `json:"from"`
	To        string `json:"to"`
	Timestamp int64  `json:"timestamp"`
	Preview   string `json:"preview"`
	Body      string `json:"body"`
	Content   string `json:"content"`
}

// GetFull 通过 Web API 获取单封邮件的完整内容(含正文)。
// threadID 即 ListInbox 返回的 Message.ID。
func (c *WebClient) GetFull(threadID string) (*FullMessage, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, fmt.Errorf("邮件 ID 无效")
	}
	if err := c.resolveMccGateway(); err != nil {
		return nil, err
	}

	payload := fmt.Sprintf(`{"threadId":%q,"sessionHeaders":{"folder":"INBOX","condstore":1,"qresync":1}}`, threadID)
	url := c.withParams(c.mccGatewayURL + "/mailws2/v1/message/batch")
	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取邮件详情失败: HTTP %d - %s", resp.StatusCode, truncate(string(body), 300))
	}
	if strings.Contains(string(body), `"success":false`) {
		return nil, fmt.Errorf("获取邮件详情失败: %s", truncate(string(body), 300))
	}

	msgs, err := parseWebMessages(body)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("邮件不存在 (threadId=%s)", threadID)
	}

	// 一个 thread 可能有多封往来,取最新一封作为"详情"
	latest := msgs[len(msgs)-1]
	for _, m := range msgs {
		if m.Timestamp > latest.Timestamp {
			latest = m
		}
	}

	full := &FullMessage{Message: Message{ID: threadID}}
	full.From = firstNonEmpty(latest.Sender, latest.From)
	full.To = latest.To
	full.Subject = latest.Subject
	if latest.Timestamp > 0 {
		full.Date = time.UnixMilli(latest.Timestamp).Format(time.RFC3339)
	}
	text := firstNonEmpty(latest.Body, latest.Content, latest.Preview)
	full.Body = sanitizePreview(text)
	full.Preview = sanitizePreview(latest.Preview)
	if strings.Contains(strings.ToLower(latest.Content), "<") {
		full.ContentType = "text/html"
	} else {
		full.ContentType = "text/plain"
	}
	return full, nil
}

// parseWebMessages 解析 message/batch 响应,兼容单封与数组两种外层结构。
func parseWebMessages(raw []byte) ([]webMessage, error) {
	var batch messageBatchResp
	if err := json.Unmarshal(raw, &batch); err == nil && len(batch.Messages) > 0 {
		return batch.Messages, nil
	}
	var list []webMessage
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	var single webMessage
	if err := json.Unmarshal(raw, &single); err == nil && (single.ID != "" || single.Subject != "") {
		return []webMessage{single}, nil
	}
	return nil, fmt.Errorf("解析邮件详情响应失败")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
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
