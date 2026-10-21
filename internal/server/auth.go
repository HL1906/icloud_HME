// Package server - 登录/会话/退出 handler 与认证中间件。
//
// auth.Manager 不依赖 Gin;此处只负责 Cookie/Header 与 HTTP 状态映射。
package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"icloud-hme/internal/auth"
)

// authManager 是 auth.Manager 的别名,便于 handler 签名。
type authManager = auth.Manager

// sessionCookieName 是管理员会话 Cookie 名称。
const sessionCookieName = "hme_session"

// sessionIDFromCookie 从请求 Cookie 提取 session ID。
func sessionIDFromCookie(c *gin.Context) string {
	if cookie, err := c.Request.Cookie(sessionCookieName); err == nil {
		return cookie.Value
	}
	return ""
}

// requireSession 校验会话,失败返回 401/AUTH_REQUIRED。
func requireSession(mgr *authManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := sessionIDFromCookie(c)
		if sessionID == "" {
			failCode(c, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
			return
		}
		if _, ok := mgr.Validate(sessionID); !ok {
			failCode(c, http.StatusUnauthorized, "AUTH_REQUIRED", "会话已失效,请重新登录")
			return
		}
		c.Set("session_id", sessionID)
		c.Next()
	}
}

// setSessionCookie 设置会话 Cookie(固定属性:Path=/、HttpOnly、SameSite=Strict)。
func setSessionCookie(c *gin.Context, sessionID string, expiresAt time.Time, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	})
}

// clearSessionCookie 清除会话 Cookie。
func clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// handleLogin 处理 POST /api/auth/login。
//
// 防爆破设计:先校验密码,密码正确时**不受限流状态影响**直接放行。
// 只有密码错误才计入限流。这样即使有人恶意刷错误密码把该 IP 打到限流阈值,
// 管理员用正确密码仍能登录(避免"全站登录被锁死"的 DoS)。
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误")
		return
	}
	// 防止超长输入拖慢 argon2 计算
	if len(req.Password) > maxPasswordBytes {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误")
		return
	}

	ip := c.ClientIP()
	blocked, retryAfter := s.limiter.Blocked(ip)

	// 密码正确:无条件放行(即使当前处于限流窗口内)
	sessionID, sess, valid := s.auth.Login(req.Password)
	if valid {
		s.limiter.Success(ip)
		setSessionCookie(c, sessionID, sess.ExpiresAt, s.cfg.SecureCookie)
		ok(c, gin.H{
			"csrf_token": sess.CSRFToken,
			"expires_at": sess.ExpiresAt.Format(time.RFC3339),
		})
		return
	}

	// 密码错误:计入限流;已处于封锁状态时提示稍后再试
	if blocked {
		c.Header("Retry-After", formatRetryAfter(retryAfter))
		failCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "登录尝试过于频繁,请稍后再试")
		return
	}
	allowed, retry := s.limiter.Allow(ip)
	if !allowed {
		c.Header("Retry-After", formatRetryAfter(retry))
		failCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "登录尝试过于频繁,请稍后再试")
		return
	}
	failCode(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "管理员密码错误")
}

// formatRetryAfter 把等待时间格式化为秒数(Retry-After 头规范)。
func formatRetryAfter(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// handleSession 处理 GET /api/auth/session。
func (s *Server) handleSession(c *gin.Context) {
	sessionID := sessionIDFromCookie(c)
	if sessionID == "" {
		failCode(c, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	sess, valid := s.auth.Validate(sessionID)
	if !valid {
		failCode(c, http.StatusUnauthorized, "AUTH_REQUIRED", "会话已失效,请重新登录")
		return
	}
	ok(c, gin.H{
		"csrf_token": sess.CSRFToken,
		"expires_at": sess.ExpiresAt.Format(time.RFC3339),
	})
}

// handleLogout 处理 POST /api/auth/logout(需会话 + CSRF)。
func (s *Server) handleLogout(c *gin.Context) {
	sessionID := sessionIDFromCookie(c)
	if sessionID != "" {
		s.auth.Logout(sessionID)
	}
	clearSessionCookie(c)
	ok(c, gin.H{"logged_out": true})
}

// handleChangePassword 处理 POST /api/auth/password。
//
// 安全要点:
//   - 必须带有效会话 + CSRF(中间件已保证)
//   - 必须校验旧密码:防止会话被劫持后直接改密码锁住真正的管理员
//   - 旧密码错误独立限流,避免借会话做在线爆破
//   - 改成功后注销其它会话,但保留当前会话
func (s *Server) handleChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体需为 JSON")
		return
	}
	if len(req.OldPassword) > maxPasswordBytes || len(req.NewPassword) > maxPasswordBytes {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 密码过长")
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 新旧密码均不能为空")
		return
	}

	// 旧密码校验失败单独限流:按客户端 IP + 会话
	ip := c.ClientIP()
	blocked, retryAfter := s.limiter.Blocked(ip)
	if blocked {
		c.Header("Retry-After", formatRetryAfter(retryAfter))
		failCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "尝试过于频繁,请稍后再试")
		return
	}

	sessionID := sessionIDFromCookie(c)
	if err := s.auth.ChangePassword(req.OldPassword, req.NewPassword, sessionID); err != nil {
		// 旧密码错误要计数,其它校验错误(新密码太弱等)不计数
		if strings.Contains(err.Error(), "当前密码不正确") {
			if allowed, retry := s.limiter.Allow(ip); !allowed {
				c.Header("Retry-After", formatRetryAfter(retry))
				failCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "尝试过于频繁,请稍后再试")
				return
			}
			failCode(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "当前密码不正确")
			return
		}
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	s.limiter.Success(ip)
	ok(c, gin.H{"changed": true})
}
