// Package server - 对外取件接口(/m)。
//
// 这是整套"隐藏邮箱分发"的收件人侧入口:管理员把别名发出去时,
// 交给对方的不是账号密码,而是一条 "邮箱:令牌" 的链接:
//
//	https://your-host/m?f=html&e=abc123@icloud.com:xwsHnDAi
//
// 令牌只对应一个别名,只授予"读这个别名的邮件",既不能改别名、
// 也看不到账号、看不到其他别名。格式与 wdmail.top 那类取件服务保持一致,
// 方便别人现成的脚本直接对接。
package server

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"icloud-hme/internal/mail"
)

// 取件接口的取值边界。
const (
	defaultPublicHours = 24
	maxPublicHours     = 168
	defaultPublicLimit = 20
	maxPublicLimit     = 50
	// publicCacheTTL 是同一别名取信的缓存时长。收件人页面会自动刷新,
	// 没有缓存的话每次刷新都是一次 IMAP 往返。
	publicCacheTTL = 10 * time.Second
)

// publicLimiter 是取件接口的滑动窗口限流器。
//
// 两把尺子:按 IP 防单机刷,按令牌防"一个链接被到处传播后被打爆"。
type publicLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	maxPerKey int
	maxGlobal int
	keys      map[string]*publicCounter
	global    *publicCounter
	now       func() time.Time
}

type publicCounter struct {
	start time.Time
	n     int
}

func newPublicLimiter(window time.Duration, maxPerKey, maxGlobal int) *publicLimiter {
	return &publicLimiter{
		window:    window,
		maxPerKey: maxPerKey,
		maxGlobal: maxGlobal,
		keys:      make(map[string]*publicCounter),
		global:    &publicCounter{},
		now:       time.Now,
	}
}

// allow 记一次请求并返回是否放行。任何一个维度超限都拒绝。
func (l *publicLimiter) allow(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	if now.Sub(l.global.start) >= l.window {
		l.global = &publicCounter{start: now}
	}
	l.global.n++
	if l.global.n > l.maxGlobal {
		return false
	}

	// 键太多说明被扫了,直接重置表(限流宁可短暂放宽,也不能无限吃内存)
	if len(l.keys) > 20000 {
		l.keys = make(map[string]*publicCounter, len(keys))
	}

	for _, key := range keys {
		c := l.keys[key]
		if c == nil || now.Sub(c.start) >= l.window {
			c = &publicCounter{start: now}
			l.keys[key] = c
		}
		c.n++
		if c.n > l.maxPerKey {
			return false
		}
	}
	return true
}

// publicCache 缓存取信结果,减少上游往返。
type publicCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]publicCacheItem
	now   func() time.Time
}

type publicCacheItem struct {
	at     time.Time
	result AliasMailResult
}

func newPublicCache(ttl time.Duration) *publicCache {
	return &publicCache{ttl: ttl, items: make(map[string]publicCacheItem), now: time.Now}
}

func (c *publicCache) get(key string) (AliasMailResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || c.now().Sub(item.at) >= c.ttl {
		return AliasMailResult{}, false
	}
	return item.result, true
}

func (c *publicCache) put(key string, result AliasMailResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) > 5000 {
		c.items = make(map[string]publicCacheItem)
	}
	c.items[key] = publicCacheItem{at: c.now(), result: result}
}

// splitAliasCredential 把 "邮箱:令牌" 拆开。用最后一个冒号,
// 避免邮箱里出现冒号(理论上不该有)时切错位置。
func splitAliasCredential(raw string) (email, token string) {
	raw = strings.TrimSpace(raw)
	i := strings.LastIndex(raw, ":")
	if i <= 0 || i == len(raw)-1 {
		return "", ""
	}
	email = strings.ToLower(strings.TrimSpace(raw[:i]))
	token = strings.TrimSpace(raw[i+1:])
	if email == "" || token == "" || len(email) > 320 || len(token) > 64 {
		return "", ""
	}
	if !strings.Contains(email, "@") {
		return "", ""
	}
	return email, token
}

// publicFail 返回取件接口的错误响应,结构与 wdmail 一致:
// {"code":404,"data":{"message":"mailbox not found"}}
func publicFail(c *gin.Context, status, code int, message string) {
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"code": code, "data": gin.H{"message": message}})
}

// revokedTokenView 是"链接已失效"页面的渲染数据。
type revokedTokenView struct {
	Recipient string
	Now       string
}

// revokedTokenTemplate 是链接失效页。
//
// 独立于取件页:只有一个居中的提示卡片,不复用邮件列表模板,
// 免得把收件箱的样式和结构带进来(那部分是有邮件才该出现的)。
var revokedTokenTemplate = template.Must(template.New("revoked-token").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta name="robots" content="noindex, nofollow">
<title>链接已失效</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    padding: 24px; background: #f0f2f5; color: #1f2329;
    font: 14px/1.6 -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  }
  .card {
    width: 100%; max-width: 420px; padding: 28px 24px; text-align: center;
    background: #fff; border: 1px solid #e5e6eb; border-radius: 6px; box-shadow: 0 1px 3px rgb(0 0 0 / 0.05);
  }
  .icon {
    width: 44px; height: 44px; margin: 0 auto 16px; display: grid; place-items: center;
    border-radius: 50%; background: #fff1ed; color: #d4380d; font-size: 22px; line-height: 1;
  }
  h1 { margin: 0 0 8px; font-size: 17px; font-weight: 600; }
  p { margin: 0 0 6px; color: #4e5969; }
  .hint { color: #86909c; font-size: 12px; }
  .recipient {
    margin: 16px 0 0; padding: 8px 12px; word-break: break-all;
    background: #f5f7fa; border: 1px solid #e5e6eb; border-radius: 4px;
    color: #4e5969; font-size: 12px; font-family: ui-monospace, Menlo, Consolas, monospace;
  }
  footer { margin-top: 18px; color: #a8b0b9; font-size: 11px; }
</style>
</head>
<body>
  <div class="card">
    <div class="icon" aria-hidden="true">!</div>
    <h1>这个链接已失效</h1>
    <p>取件链接已到期或被回收，无法再查看邮件。</p>
    <p class="hint">如果还需要查看，请联系发件人重新发送一个链接。</p>
    {{if .Recipient}}<div class="recipient">{{.Recipient}}</div>{{end}}
    <footer>{{.Now}}</footer>
  </div>
</body>
</html>
`))

// respondRevokedToken 回应"令牌曾经有效、现在已作废"的请求。
//
// HTML 直接给一张"链接已失效"的卡片;JSON 用 code=410 表达同一件事
// (Gone:资源曾经存在,现在没了),方便脚本区分"没这个链接"和"链接过期"。
func (s *Server) respondRevokedToken(c *gin.Context, email, format string) {
	c.Header("Cache-Control", "no-store")
	if format == "json" {
		c.JSON(http.StatusGone, gin.H{
			"code": http.StatusGone,
			"data": gin.H{"message": "link expired or revoked"},
		})
		return
	}
	view := revokedTokenView{
		Recipient: email,
		Now:       time.Now().Format("2006-01-02 15:04:05"),
	}
	var buf bytes.Buffer
	if err := revokedTokenTemplate.Execute(&buf, view); err != nil {
		// 模板出错就退回最朴素的一行文本:这里绝不能因为渲染失败变成空白页
		c.String(http.StatusGone, "这个链接已失效")
		return
	}
	c.Data(http.StatusGone, "text/html; charset=utf-8", buf.Bytes())
}

// publicMailView 是 HTML 页面的渲染数据。
type publicMailView struct {
	Recipient string
	Hours     int
	Count     int
	Messages  []publicMailItem
	Error     string
	// AutoRefresh 只在"还没有邮件"时开启:有邮件后停在当前页,
	// 免得收件人正在读验证码时被刷新打断。
	AutoRefresh bool
	Now         string
}

type publicMailItem struct {
	Subject string
	From    string
	To      string
	Date    string
	Body    string
}

// publicMailTemplate 是取件页面。刻意做成单文件、零脚本、零外链:
// 收件人可能在任何设备上打开,而 CSP 只允许内联样式。
var publicMailTemplate = template.Must(template.New("public-mail").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta name="robots" content="noindex, nofollow">
{{if .AutoRefresh}}<meta http-equiv="refresh" content="20">{{end}}
<title>{{.Recipient}} · 邮箱邮件</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 16px; font: 15px/1.6 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; background: #f6f7f9; color: #1c1c1e; }
  header { max-width: 720px; margin: 0 auto 14px; }
  h1 { font-size: 17px; margin: 0 0 4px; word-break: break-all; }
  .sub { margin: 0; color: #6b7280; font-size: 13px; }
  .card { max-width: 720px; margin: 0 auto 12px; background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 14px 16px; }
  .card h2 { font-size: 15px; margin: 0 0 6px; word-break: break-word; }
  .meta { color: #6b7280; font-size: 12px; margin-bottom: 10px; word-break: break-all; }
  pre { margin: 0; white-space: pre-wrap; word-break: break-word; font: 14px/1.7 ui-monospace, "SFMono-Regular", Consolas, monospace; }
  .empty { max-width: 720px; margin: 0 auto; padding: 28px 16px; text-align: center; color: #6b7280; background: #fff; border: 1px dashed #d1d5db; border-radius: 12px; }
  .err { max-width: 720px; margin: 0 auto 12px; padding: 10px 14px; border-radius: 10px; background: #fef2f2; color: #b91c1c; font-size: 13px; }
  footer { max-width: 720px; margin: 18px auto 0; color: #9ca3af; font-size: 12px; text-align: center; }
  a { color: #2563eb; }
  @media (prefers-color-scheme: dark) {
    body { background: #111315; color: #e5e7eb; }
    .card, .empty { background: #1a1d21; border-color: #2b3036; }
    .sub, .meta, footer { color: #9aa3ae; }
    .err { background: #2a1416; color: #fca5a5; }
    a { color: #60a5fa; }
  }
</style>
</head>
<body>
<header>
  <h1>{{.Recipient}}</h1>
  <p class="sub">最近 {{.Hours}} 小时邮件 · 共 {{.Count}} 封{{if .AutoRefresh}} · 有新邮件会自动刷新{{end}}</p>
</header>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if .Messages}}
{{range .Messages}}
<article class="card">
  <h2>{{if .Subject}}{{.Subject}}{{else}}(无主题){{end}}</h2>
  <div class="meta">{{if .From}}来自 {{.From}} · {{end}}{{.Date}}</div>
  <pre>{{.Body}}</pre>
</article>
{{end}}
{{else if not .Error}}
<p class="empty">最近 {{.Hours}} 小时没有邮件{{if .AutoRefresh}}，收到后会自动显示{{end}}</p>
{{end}}
<footer>{{.Now}} · <a href="">刷新</a></footer>
</body>
</html>
`))

// handlePublicMail 处理 GET /m?f=html|json&e=邮箱:令牌[&h=24][&limit=20]。
//
// 鉴权只靠 URL 里的令牌:没有会话、没有 Cookie、不碰管理员权限。
// 任何一处对不上(令牌不存在、邮箱与令牌不匹配、账号已删除)都返回同一个
// 404 "mailbox not found",不告诉对方是"邮箱错"还是"令牌错"。
func (s *Server) handlePublicMail(c *gin.Context) {
	// 取件页面自带内联样式,需要单独放宽 style-src;脚本仍然完全禁止。
	c.Header("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")

	format := strings.ToLower(strings.TrimSpace(c.Query("f")))
	if format == "" {
		format = "html"
	}
	if format != "html" && format != "json" {
		publicFail(c, http.StatusBadRequest, http.StatusBadRequest, "f must be json or html")
		return
	}

	email, token := splitAliasCredential(c.Query("e"))
	if email == "" || token == "" {
		publicFail(c, http.StatusNotFound, http.StatusNotFound, "mailbox not found")
		return
	}

	if !s.pubLimiter.allow("ip:"+c.ClientIP(), "tok:"+token) {
		publicFail(c, http.StatusTooManyRequests, http.StatusTooManyRequests, "too many requests")
		return
	}

	entry, found := s.meta.ByToken(token)
	if !found || entry.AccountID == "" || !strings.EqualFold(entry.Email, email) {
		// 令牌查不到:区分"从未存在"和"曾经有效但已作废"。
		// 后者给出明确提示,免得对方以为是自己地址打错了;
		// 前者仍是统一的 404,不透露任何信息。
		if revokedEmail, revoked := s.meta.RevokedToken(token); revoked && strings.EqualFold(revokedEmail, email) {
			s.respondRevokedToken(c, revokedEmail, format)
			return
		}
		publicFail(c, http.StatusNotFound, http.StatusNotFound, "mailbox not found")
		return
	}

	// 默认值来自设置页,可随时调整;显式传参优先
	cur := s.settings.Get()
	defHours := clampInt(cur.PublicMailHours, 1, maxPublicHours, defaultPublicHours)
	defLimit := clampInt(cur.PublicMailLimit, 1, maxPublicLimit, defaultPublicLimit)
	hours := parsePublicInt(c.DefaultQuery("h", strconv.Itoa(defHours)), 1, maxPublicHours, defHours)
	limit := parsePublicInt(c.DefaultQuery("limit", strconv.Itoa(defLimit)), 1, maxPublicLimit, defLimit)

	result, err := s.publicMails(entry.Email, entry.AccountID, hours, limit)
	if format == "json" {
		if err != nil {
			publicFail(c, http.StatusBadGateway, http.StatusBadGateway, "upstream unavailable")
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"recipient":        entry.Email,
				"mail_query_hours": hours,
				"count":            len(result.Messages),
				"messages":         result.Messages,
			},
		})
		return
	}

	view := publicMailView{
		Recipient: entry.Email,
		Hours:     hours,
		Count:     len(result.Messages),
		Now:       time.Now().Format("2006-01-02 15:04:05"),
	}
	if err != nil {
		view.Error = "读取邮件失败，请稍后刷新重试"
		view.AutoRefresh = true
	} else {
		view.AutoRefresh = len(result.Messages) == 0
		for _, m := range result.Messages {
			body := strings.TrimSpace(m.Body)
			if body == "" {
				body = strings.TrimSpace(m.Preview)
				if body != "" {
					body += "…"
				}
			}
			if body == "" {
				body = "(无正文)"
			}
			view.Messages = append(view.Messages, publicMailItem{
				Subject: m.Subject,
				From:    m.From,
				To:      m.To,
				Date:    formatPublicDate(m.Date),
				Body:    body,
			})
		}
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := publicMailTemplate.Execute(c.Writer, view); err != nil {
		// 响应头已经发出去了,这里只能记日志,不能再改状态码
		c.Error(err) //nolint:errcheck // gin 会把错误收集到上下文里
	}
}

// publicMails 取信,带短时缓存。
func (s *Server) publicMails(email, accountID string, hours, limit int) (AliasMailResult, error) {
	key := email + "|" + strconv.Itoa(hours) + "|" + strconv.Itoa(limit)
	if cached, ok := s.pubCache.get(key); ok {
		return cached, nil
	}
	result, err := s.be.FetchAliasMails(accountID, email, hours, limit, true)
	if err != nil {
		return AliasMailResult{}, err
	}
	if result.Messages == nil {
		result.Messages = []AliasMail{}
	}
	s.pubCache.put(key, result)
	return result, nil
}

// parsePublicInt 解析正整数参数,非法时回落到默认值。
// 对外接口不该因为一个畸形参数就报错——收件人只是想看邮件。
func parsePublicInt(raw string, min, max, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < min || v > max {
		return def
	}
	return v
}

// clampInt 把设置里读到的值夹到合法区间,越界时用默认值。
//
// 设置存盘前已经校验过,这里再兜一次:settings.json 可能被人手工改过,
// 而取件页是对外的,不能让它因为一个坏数字变成 500。
func clampInt(v, min, max, def int) int {
	if v < min || v > max {
		return def
	}
	return v
}

// formatPublicDate 把邮件时间转成本地可读格式。
func formatPublicDate(raw string) string {
	if at := mail.ParseMessageDate(raw); !at.IsZero() {
		return at.Local().Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

// publicBaseURL 推导取件链接的站点前缀。
//
// 优先级:设置页填的对外域名 > ICLOUD_HME_PUBLIC_BASE_URL > 按当前请求推导。
// 设置页排在最前面,是为了让"改完立刻生效"这件事成立——环境变量改了要重启。
// 不读 X-Forwarded-Proto:该头可被任意客户端伪造,而这里生成的链接会直接
// 发给收件人,不能让它被请求头左右。TLS 部署由 SecureCookie 配置标记。
func (s *Server) publicBaseURL(c *gin.Context) string {
	if base := strings.TrimSpace(s.settings.Get().PublicBaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	if base := strings.TrimSpace(s.cfg.PublicBaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	scheme := "http"
	if c.Request.TLS != nil || s.cfg.SecureCookie {
		scheme = "https"
	}
	host := c.Request.Host
	if host == "" {
		host = "127.0.0.1:8081"
	}
	return scheme + "://" + host
}

// publicAliasURL 拼出某个别名的取件链接(分发格式里的 API 部分)。
//
// 只转义真正会破坏 query 的字符(& # + % 空格),再把 @ 和 : 还原成可读形式——
// 分发出去的字符串要能人工抄写,和 wdmail 的 "e=邮箱:令牌" 长得一样。
func publicAliasURL(base, email, token string) string {
	cred := url.QueryEscape(email + ":" + token)
	cred = strings.ReplaceAll(cred, "%40", "@")
	cred = strings.ReplaceAll(cred, "%3A", ":")
	return base + "/m?f=html&e=" + cred
}
