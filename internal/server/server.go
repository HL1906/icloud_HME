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
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"icloud-hme/internal/account"
	"icloud-hme/internal/aliasmeta"
	"icloud-hme/internal/auth"
	"icloud-hme/internal/forward"
	"icloud-hme/internal/pool"
	"icloud-hme/internal/settings"
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
	// PublicBaseURL 是分发出去的取件链接前缀(如 https://mail.example.com)。
	// 为空时按请求的 Host 推导。
	PublicBaseURL string
}

// Server 封装 Gin 引擎、账号后端与认证。
type Server struct {
	be      Backend
	auth    *auth.Manager
	limiter *auth.Limiter
	cfg     Config
	r       *gin.Engine
	// pool 是别名池自动补号调度器;初始化失败时为 nil(功能不可用,不影响主流程)
	pool *pool.Pool
	// meta 保存本地分组与别名备注(系统自有数据,不同步 iCloud)
	meta *aliasmeta.Store
	// scanMu/scanning 防止同一账号并发扫描收件箱(纯浪费上游连接)
	scanMu   sync.Mutex
	scanning map[string]bool
	// 取件接口(/m)的限流与短时缓存:对外暴露,必须自带防刷
	pubLimiter *publicLimiter
	pubCache   *publicCache
	// 取件链接的自动回收协程(仅在 Run 时启动)
	sweepMu   sync.Mutex
	sweepStop chan struct{}
	// settings 是运行时可改的设置(自动回收开关/间隔、取件页默认值、对外域名)
	settings *settings.Store
	// createLimiter 限制每个账号每分钟的建号数,保护上游不被风控
	createLimiter *createLimiter
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
		be:       be,
		limiter:  auth.NewLimiter(nil, 15*time.Minute, 5, 10000).WithGlobalLimit(300),
		cfg:      cfg,
		scanning: make(map[string]bool),
		// 取件接口对外:按 IP / 令牌各 60 次每分钟,全站 600 次每分钟
		pubLimiter: newPublicLimiter(time.Minute, 60, 600),
		pubCache:   newPublicCache(publicCacheTTL),
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

	// 分组与别名备注:纯本系统数据,没有数据目录时退化为内存(重启即丢)。
	meta, err := aliasmeta.NewStore(cfg.DataDir)
	if err != nil {
		log.Printf("[server] 分组存储初始化失败,分组功能降级为内存: %v", err)
		meta, _ = aliasmeta.NewStore("")
	}
	s.meta = meta

	// 运行时设置:自动回收开关/间隔、取件页默认值、对外域名。
	settingsStore, serr := settings.NewStore(cfg.DataDir)
	if serr != nil {
		log.Printf("[server] 设置存储初始化失败,改用默认设置: %v", serr)
		settingsStore, _ = settings.NewStore("")
	}
	s.settings = settingsStore
	s.createLimiter = newCreateLimiter()

	// 别名池自动补号:配置和数据都落 DataDir。
	// 没有数据目录就不启用,避免把 alias_pool.json 写进当前工作目录。
	if cfg.DataDir == "" {
		log.Printf("[server] 未配置数据目录,自动补号不可用")
	} else if pl, perr := pool.New(cfg.DataDir, poolAdapter{be: be, meta: meta}); perr != nil {
		log.Printf("[server] 别名池调度器初始化失败,自动补号不可用: %v", perr)
	} else {
		s.pool = pl
		pl.Start()
	}
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
	// 到期取件链接的后台回收:只在真正跑服务时启动,测试里不起协程
	s.startTokenSweeper()
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

// Close 停止 Server 持有的后台任务。
//
// 目前只有补号调度器需要收尾:它可能在创建间隔里等待,
// 不主动停会让进程退出时多等几秒。
func (s *Server) Close() {
	s.stopTokenSweeper()
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Server) register() {
	// ===== 对外取件接口(公开,只认 URL 里的令牌) =====
	// 路径刻意做成 /m 而不是 /api/...,与分发出去的链接格式保持一致;
	// 它不属于任何管理员会话,自带限流与缓存。
	s.r.GET("/m", s.handlePublicMail)
	s.r.HEAD("/m", s.handlePublicMail)

	api := s.r.Group("/api")
	api.Use(apiCacheControlMiddleware())
	{
		// ===== 健康检查(公开,不含敏感信息) =====
		api.GET("/healthz", s.handleHealthz)

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
			// 批量操作放在 /alias-bulk 而非 /aliases/bulk:
			// 后者会和上面 /aliases/:id/* 的参数节点混在同一段路由树里。
			authed.POST("/alias-bulk", csrfCheck(s.auth), s.bulkAliasHandler)

			// ===== 分组(本系统自有数据,不同步 iCloud) =====
			authed.GET("/groups", s.listGroupsHandler)
			authed.POST("/groups", csrfCheck(s.auth), s.createGroupHandler)
			authed.PATCH("/groups/:id", csrfCheck(s.auth), s.updateGroupHandler)
			authed.DELETE("/groups/:id", csrfCheck(s.auth), s.deleteGroupHandler)

			// ===== 标注(本系统自有的第二套标签) =====
			authed.GET("/tags", s.listTagsHandler)
			authed.POST("/tags", csrfCheck(s.auth), s.createTagHandler)
			authed.PATCH("/tags/:id", csrfCheck(s.auth), s.updateTagHandler)
			authed.DELETE("/tags/:id", csrfCheck(s.auth), s.deleteTagHandler)
			authed.PUT("/alias-tags", csrfCheck(s.auth), s.setAliasTagsHandler)

			// ===== 别名元数据(分组/备注) =====
			authed.GET("/alias-meta", s.listAliasMetaHandler)
			authed.PUT("/alias-meta", csrfCheck(s.auth), s.putAliasMetaHandler)
			authed.DELETE("/alias-meta", csrfCheck(s.auth), s.deleteAliasMetaHandler)

			// ===== 分发凭证:取件令牌(签发/撤销) =====
			// 同样避开 /aliases/:id 的参数节点,单独挂在 /alias-token 下
			authed.POST("/alias-token", csrfCheck(s.auth), s.issueAliasTokenHandler)
			authed.DELETE("/alias-token", csrfCheck(s.auth), s.revokeAliasTokenHandler)
			// 手动触发一次到期回收(后台每 10 分钟也会自动跑)
			authed.POST("/alias-token/sweep", csrfCheck(s.auth), s.sweepTokensHandler)

			// ===== 收信活动(最近一次收到邮件的时间) =====
			authed.GET("/alias-activity", s.aliasActivityHandler)
			authed.POST("/alias-activity/scan", csrfCheck(s.auth), s.scanAliasActivityHandler)

			// ===== 别名池自动补号 =====
			authed.GET("/pool", s.listPoolHandler)
			authed.GET("/pool/:id/preview", s.previewPoolHandler)
			authed.PUT("/pool/:id", csrfCheck(s.auth), s.updatePoolHandler)
			authed.DELETE("/pool/:id", csrfCheck(s.auth), s.deletePoolHandler)
			authed.POST("/pool/:id/run", csrfCheck(s.auth), s.runPoolHandler)

			// ===== 系统 =====
			authed.GET("/meta", s.handleMeta)
			authed.GET("/settings", s.getSettingsHandler)
			authed.PUT("/settings", csrfCheck(s.auth), s.updateSettingsHandler)
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
	GroupID   string `json:"group_id"`
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
	// 分组先校验:别等邮箱建好了才发现分组 ID 是错的,那已经消耗了一个别名额度
	if req.GroupID != "" && !s.meta.GroupExists(req.GroupID) {
		failCode(c, http.StatusBadRequest, "GROUP_NOT_FOUND", "分组不存在")
		return
	}
	// 限流放在所有校验之后、真正建号之前:参数错的请求不该占额度
	if !s.allowCreate(c, req.AccountID) {
		return
	}

	result, err := s.be.CreateAlias(req.AccountID, req.Label)
	if err != nil {
		backendFail(c, err)
		return
	}
	if req.GroupID != "" {
		// 归组失败不影响创建结果:邮箱已经在 iCloud 上了,用户可以再手工编辑
		if _, gerr := s.meta.Upsert(result.Email, req.AccountID, req.GroupID, ""); gerr != nil {
			log.Printf("[server] 新建别名归组失败 email=%s: %v", result.Email, gerr)
		}
	}
	ok(c, gin.H{
		"email":      result.Email,
		"label":      result.Label,
		"created_at": result.CreatedAt,
		"account_id": req.AccountID,
		"group_id":   req.GroupID,
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
	// 账号必须真实存在:否则任何字符串都会被当成"某个账号"转发给上游,
	// 既掩盖了前端 bug,也让错误信息变成看不懂的上游报错。
	if !s.accountExists(accountID) {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
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
	// 传了 alias 就必须确实属于这个账号:否则等于让调用方
	// 用任意别名的地址去上游碰运气,拿到一个空结果还以为是"没收到信"。
	if alias != "" {
		if !s.aliasOwned(c, accountID, alias) {
			return
		}
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
	if !s.accountExists(accountID) {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	aliases, err := s.listAliasesFor(c, accountID)
	if err != nil {
		backendFail(c, err)
		return
	}
	// 把本地分组/备注一起带上,省去前端再请求一次;没有元数据的别名不出现在 map 里
	meta := make(map[string]aliasmeta.Entry, len(aliases))
	for _, alias := range aliases {
		if entry, ok := s.meta.Get(alias.Email); ok {
			meta[entry.Email] = entry
		}
	}
	ok(c, gin.H{
		"account_id": accountID,
		"count":      len(aliases),
		"aliases":    aliases,
		"meta":       meta,
	})
}

// aliasActionReq 是别名操作请求体。Email 仅用于顺带清理本地元数据,可空。
type aliasActionReq struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
}

// validateAliasAction 校验别名操作的匿名 ID 与请求体。
func validateAliasAction(c *gin.Context) (accountID, anonymousID, email string, valid bool) {
	anonymousID = c.Param("id")
	var req aliasActionReq
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return "", "", "", false
	}
	if anonymousID == "" || len(anonymousID) > 256 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: anonymous id 无效")
		return "", "", "", false
	}
	if len(req.Email) > aliasmeta.MaxEmailLen {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: email 无效")
		return "", "", "", false
	}
	return req.AccountID, anonymousID, req.Email, true
}

func (s *Server) deactivateAliasHandler(c *gin.Context) {
	accountID, anonymousID, _, valid := validateAliasAction(c)
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
	accountID, anonymousID, _, valid := validateAliasAction(c)
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
	accountID, anonymousID, email, valid := validateAliasAction(c)
	if !valid {
		return
	}
	if err := s.be.DeleteAlias(accountID, anonymousID); err != nil {
		backendFail(c, err)
		return
	}
	// 别名已经不在 iCloud 上了,本地分组/备注一并清掉,避免元数据长期堆积
	if email != "" {
		if _, err := s.meta.DeleteEntry(email); err != nil {
			log.Printf("[server] 清理别名元数据失败 email=%s: %v", email, err)
		}
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

// Version 是构建版本,由 -ldflags "-X icloud-hme/internal/server.Version=v1.2.3" 注入。
// 没注入时保持 "dev",页面照常显示。
var Version = "dev"

// handleMeta 处理 GET /api/meta,返回不影响安全的运行模式信息。
func (s *Server) handleMeta(c *gin.Context) {
	ok(c, gin.H{
		"version": Version,
		// 前端据此拼"邮箱----API"分发串;反代部署时用 ICLOUD_HME_PUBLIC_BASE_URL 覆盖
		"public_base_url": s.publicBaseURL(c),
	})
}
