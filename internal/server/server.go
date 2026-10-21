// Package server 提供 HTTP API,基于 Gin。
//
// 两个核心接口:
//
//	POST /api/create  — 在指定账号下创建一个 Hide My Email 别名
//	GET  /api/inbox   — 读取指定账号(或指定别名)收到的邮件
//
// 辅助接口(用于多账号管理):账号增删查、别名列表、设置 App 密码。
//
// 安全模型:除 /api/auth/login 与 /api/auth/session 外,所有 /api 路由都需要
// 管理员会话;非 GET/HEAD/OPTIONS 请求还需校验 CSRF。
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"icloud-hme/internal/account"
	"icloud-hme/internal/auth"
	"icloud-hme/internal/forward"
	"icloud-hme/internal/webui"
)

// Config 是 Server 的启动配置。
type Config struct {
	Debug         bool
	AdminPassword string
	SessionTTL    time.Duration
	SecureCookie  bool
	// DataDir 是持久化目录(转发标记等)。为空时使用当前目录。
	DataDir string
}

// Server 封装 Gin 引擎、账号后端与认证。
type Server struct {
	be      Backend
	auth    *auth.Manager
	limiter *auth.Limiter
	cfg     Config
	r       *gin.Engine
}

// New 创建 Server。mgr 为账号管理器,cfg 为安全配置。
func New(mgr *account.Manager, cfg Config) (*Server, error) {
	be := &managerBackend{mgr: mgr}
	// 转发标记存储不可用不应阻止启动,降级为"不去重";
	// 但必须留日志,否则转发会静默重复推送。
	if store, err := forward.NewStore(cfg.DataDir); err == nil {
		be.forward = store
	} else {
		log.Printf("[server] 转发标记存储不可用,转发去重已禁用: %v", err)
	}
	return newWithBackend(be, cfg)
}

// credentialFile 返回密码凭据的持久化路径(为空表示不持久化)。
func (c Config) credentialFile() string {
	if c.DataDir == "" {
		return ""
	}
	return filepath.Join(c.DataDir, "admin_credential.json")
}

// newWithBackend 创建 Server 并注入 Backend(测试使用内存 fake)。
func newWithBackend(be Backend, cfg Config) (*Server, error) {
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	s := &Server{
		be:      be,
		limiter: auth.NewLimiter(nil, 15*time.Minute, 5, 10000).WithGlobalLimit(300),
		cfg:     cfg,
	}
	authMgr, err := auth.NewManager(auth.Options{
		Password:       cfg.AdminPassword,
		TTL:            cfg.SessionTTL,
		CredentialFile: cfg.credentialFile(),
	})
	if err != nil {
		return nil, err
	}
	s.auth = authMgr
	s.r = gin.New()
	// 不记录 query string:别名等隐私信息不应进入访问日志
	s.r.Use(gin.LoggerWithFormatter(redactedLogFormatter), gin.Recovery(),
		securityHeadersWithHSTS(cfg.SecureCookie), bodyLimitMiddleware())
	// 不信任任意代理头,登录限流使用真实连接 IP
	_ = s.r.SetTrustedProxies(nil)
	s.register()
	return s, nil
}

// redactedLogFormatter 输出不含 query string 的访问日志。
//
// /api/inbox?alias=xxx@icloud.com 这类请求会把隐私别名写进日志,
// 因此这里只记录路径部分。
func redactedLogFormatter(p gin.LogFormatterParams) string {
	return fmt.Sprintf("[GIN] %v | %3d | %13v | %15s | %-7s %s\n",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"),
		p.StatusCode,
		p.Latency,
		p.ClientIP,
		p.Method,
		p.Path, // Path 不含 query string
	)
}

// shutdownTimeout 是优雅关闭时等待在途请求完成的最长时间。
const shutdownTimeout = 10 * time.Second

// Run 启动 HTTP 服务,并在收到 SIGINT/SIGTERM 后优雅关闭。
//
// 显式设置各阶段超时,避免慢连接(Slowloris)长期占用连接池。
// 优雅关闭保证在途请求处理完再退出,不会把写了一半的响应或配置留在磁盘上。
func (s *Server) Run(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10, // 64 KiB
	}

	// 监听失败的错误通过 channel 回传,避免在 goroutine 里直接退出进程
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Printf("收到信号 %s,开始优雅关闭", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			// 超时后强关连接,保证进程能退出
			_ = srv.Close()
			return fmt.Errorf("优雅关闭超时: %w", err)
		}
		log.Printf("HTTP 服务已关闭")
		return nil
	}
}

// Handler 返回底层 gin 引擎(便于测试)。
func (s *Server) Handler() http.Handler { return s.r }

func (s *Server) register() {
	api := s.r.Group("/api")
	api.Use(apiCacheControlMiddleware())
	{
		// ===== 认证(公开) =====
		api.POST("/auth/login", s.handleLogin)
		api.GET("/auth/session", s.handleSession)

		// ===== 受保护路由:统一 requireSession =====
		authed := api.Group("")
		authed.Use(requireSession(s.auth))
		{
			authed.POST("/auth/logout", csrfCheck(s.auth), s.handleLogout)
			// 修改管理员密码(需会话 + CSRF + 旧密码校验)
			authed.POST("/auth/password", csrfCheck(s.auth), s.handleChangePassword)

			// ===== 账号管理 =====
			authed.GET("/accounts", s.listAccountsHandler)
			authed.POST("/accounts", csrfCheck(s.auth), s.addAccountHandler)
			authed.PATCH("/accounts/:id", csrfCheck(s.auth), s.updateAccountHandler)
			authed.PUT("/accounts/:id/proxy", csrfCheck(s.auth), s.updateProxyHandler)
			authed.PUT("/accounts/:id/cookies", csrfCheck(s.auth), s.updateCookiesHandler)
			authed.POST("/accounts/:id/password", csrfCheck(s.auth), s.setAppPasswordHandler)
			authed.PUT("/accounts/:id/mailbox", csrfCheck(s.auth), s.setMailboxHandler)
			authed.POST("/accounts/:id/login", csrfCheck(s.auth), s.loginAccountHandler)
			authed.DELETE("/accounts/:id", csrfCheck(s.auth), s.removeAccountHandler)

			// ===== 核心接口 1: 创建邮箱 =====
			authed.POST("/create", csrfCheck(s.auth), s.createAliasHandler)

			// ===== 核心接口 2: 读取邮件 =====
			authed.GET("/inbox", s.listInboxHandler)
			authed.GET("/inbox/:message_id", s.getMessageHandler)
			authed.DELETE("/inbox/:message_id", csrfCheck(s.auth), s.deleteMessageHandler)

			// ===== 转发服务 =====
			authed.GET("/forward", s.listForwardHandler)
			authed.POST("/forward/ack", csrfCheck(s.auth), s.ackForwardHandler)

			// ===== 别名管理 =====
			authed.GET("/aliases", s.listAliasesHandler)
			authed.POST("/aliases/:id/deactivate", csrfCheck(s.auth), s.deactivateAliasHandler)
			authed.POST("/aliases/:id/reactivate", csrfCheck(s.auth), s.reactivateAliasHandler)
			authed.DELETE("/aliases/:id", csrfCheck(s.auth), s.deleteAliasHandler)

			// ===== 系统 =====
			authed.POST("/reload", csrfCheck(s.auth), s.reloadConfigHandler)
		}
	}
	// API 404 返回 JSON,绝不让 NoRoute 把拼错的 API 路径变成 HTML
	s.r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			failCode(c, http.StatusNotFound, "VALIDATION_ERROR", "接口不存在")
			return
		}
		// 其余路径交给 webui(SPA fallback)
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.String(http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		webui.Handler(webuiFS).ServeHTTP(c.Writer, c.Request)
	})
}

// webuiFS 是内嵌前端资源(可被测试替换)。
var webuiFS = func() fs.FS {
	f, err := webui.Embedded()
	if err != nil {
		return nil
	}
	return f
}()

// ====================================================================
// 核心接口 1: 创建邮箱
//   POST /api/create
//   body: {"account_id": "acc_xxx", "label": "可选标签"}
//   返回: 新创建的 HME 邮箱地址
// ====================================================================

type createAliasReq struct {
	AccountID string `json:"account_id"`
	Label     string `json:"label"`
}

func (s *Server) createAliasHandler(c *gin.Context) {
	var req createAliasReq
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return
	}
	if len([]rune(req.Label)) > 200 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: label 最长 200 字符")
		return
	}

	result, err := s.be.CreateAlias(req.AccountID, req.Label)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{
		"email":      result.Email,
		"label":      result.Label,
		"created_at": result.CreatedAt,
		"account_id": req.AccountID,
	})
}

// ====================================================================
// 核心接口 2: 读取邮件
//   GET /api/inbox?account_id=acc_xxx[&alias=xxx@icloud.com][&limit=20][&days=7]
//
//   - 不传 alias: 返回该账号收件箱最近邮件
//   - 传 alias:   只返回发给该 HME 别名的邮件
//
//   认证优先级: IMAP (App Password) 优先 > Web API (Cookie) 回退
// ====================================================================

func (s *Server) listInboxHandler(c *gin.Context) {
	accountID := c.Query("account_id")
	if accountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: account_id")
		return
	}
	alias := strings.TrimSpace(c.Query("alias"))
	limit, err := parseInboxInt(c.DefaultQuery("limit", "20"), 1, 100)
	if err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: limit 需为 1-100 的整数")
		return
	}
	days, err := parseInboxInt(c.DefaultQuery("days", "7"), 1, 90)
	if err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: days 需为 1-90 的整数")
		return
	}

	result, err := s.be.ListInbox(InboxQuery{
		AccountID: accountID,
		Alias:     alias,
		Limit:     limit,
		Days:      days,
	})
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, result)
}

func (s *Server) getMessageHandler(c *gin.Context) {
	accountID := c.Query("account_id")
	messageID := strings.TrimSpace(c.Param("message_id"))
	if accountID == "" || messageID == "" || len(messageID) > 512 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "account_id 或邮件 ID 无效")
		return
	}
	message, err := s.be.GetMessage(accountID, messageID)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, message)
}

func (s *Server) deleteMessageHandler(c *gin.Context) {
	accountID := c.Query("account_id")
	messageID := strings.TrimSpace(c.Param("message_id"))
	if accountID == "" || messageID == "" || len(messageID) > 512 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "account_id 或邮件 ID 无效")
		return
	}
	if err := s.be.DeleteMessage(accountID, messageID); err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"id": messageID})
}

// parseInboxInt 解析整数参数,非法或越界返回错误(不再静默变成 0)。
func parseInboxInt(raw string, min, max int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, errors.New("invalid integer")
	}
	if v < min || v > max {
		return 0, errors.New("out of range")
	}
	return v, nil
}

// ====================================================================
// 转发服务
//   GET  /api/forward?account_id=acc[&alias=x@icloud.com][&limit=20][&days=7][&include_body=1]
//        按别名返回"尚未转发"的新邮件
//   POST /api/forward/ack
//        {"account_id":"acc","alias":"x@icloud.com","ids":["1","2"],"forwarded":true}
//        把自己的转发结果登记为已转发,下次轮询不会再返回
// ====================================================================

func (s *Server) listForwardHandler(c *gin.Context) {
	accountID := strings.TrimSpace(c.Query("account_id"))
	if accountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: account_id")
		return
	}
	alias := strings.TrimSpace(c.Query("alias"))
	if alias == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: alias")
		return
	}
	if len([]rune(alias)) > 320 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: alias 过长")
		return
	}
	limit, err := parseInboxInt(c.DefaultQuery("limit", "20"), 1, 100)
	if err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: limit 需为 1-100 的整数")
		return
	}
	days, err := parseInboxInt(c.DefaultQuery("days", "7"), 1, 90)
	if err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: days 需为 1-90 的整数")
		return
	}
	includeBody := c.Query("include_body") == "1" || c.Query("include_body") == "true"

	result, err := s.be.FetchForForward(ForwardQuery{
		AccountID:   accountID,
		Alias:       alias,
		Limit:       limit,
		Days:        days,
		IncludeBody: includeBody,
	})
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, result)
}

type ackForwardReq struct {
	AccountID string   `json:"account_id"`
	Alias     string   `json:"alias"`
	IDs       []string `json:"ids"`
	Forwarded *bool    `json:"forwarded"`
}

func (s *Server) ackForwardHandler(c *gin.Context) {
	var req ackForwardReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体需为 JSON")
		return
	}
	if strings.TrimSpace(req.AccountID) == "" || strings.TrimSpace(req.Alias) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 与 alias 必填")
		return
	}
	if len(req.IDs) == 0 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: ids 不能为空")
		return
	}
	if len(req.IDs) > 200 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 单次最多 200 个 ID")
		return
	}
	for _, id := range req.IDs {
		if len(id) > 512 {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 邮件 ID 过长")
			return
		}
	}

	forwarded := true
	if req.Forwarded != nil {
		forwarded = *req.Forwarded
	}
	n, err := s.be.MarkForwarded(req.AccountID, req.Alias, req.IDs, forwarded)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"account_id": req.AccountID, "alias": req.Alias, "updated": n, "forwarded": forwarded})
}

// ====================================================================
// 辅助接口:别名
// ====================================================================

func (s *Server) listAliasesHandler(c *gin.Context) {
	accountID := c.Query("account_id")
	if accountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: account_id")
		return
	}
	aliases, err := s.be.ListAliases(accountID)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{
		"account_id": accountID,
		"count":      len(aliases),
		"aliases":    aliases,
	})
}

type aliasActionReq struct {
	AccountID string `json:"account_id"`
}

// validateAliasAction 校验别名操作的匿名 ID 与请求体。
func validateAliasAction(c *gin.Context) (accountID, anonymousID string, valid bool) {
	anonymousID = c.Param("id")
	var req aliasActionReq
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return "", "", false
	}
	if anonymousID == "" || len(anonymousID) > 256 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: anonymous id 无效")
		return "", "", false
	}
	return req.AccountID, anonymousID, true
}

func (s *Server) deactivateAliasHandler(c *gin.Context) {
	accountID, anonymousID, valid := validateAliasAction(c)
	if !valid {
		return
	}
	success, err := s.be.SetAliasActive(accountID, anonymousID, false)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"anonymous_id": anonymousID, "success": success})
}

func (s *Server) reactivateAliasHandler(c *gin.Context) {
	accountID, anonymousID, valid := validateAliasAction(c)
	if !valid {
		return
	}
	success, err := s.be.SetAliasActive(accountID, anonymousID, true)
	if err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"anonymous_id": anonymousID, "success": success})
}

func (s *Server) deleteAliasHandler(c *gin.Context) {
	accountID, anonymousID, valid := validateAliasAction(c)
	if !valid {
		return
	}
	if err := s.be.DeleteAlias(accountID, anonymousID); err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"anonymous_id": anonymousID})
}

// ====================================================================
// 系统
// ====================================================================

func (s *Server) reloadConfigHandler(c *gin.Context) {
	if err := s.be.Reload(); err != nil {
		backendFail(c, err)
		return
	}
	ok(c, gin.H{"message": "配置已重新加载"})
}
