// Package server - 安全中间件:请求上限、安全响应头、CSRF 校验。
package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes 是 JSON 请求体上限。
const maxBodyBytes = 1 << 20 // 1 MiB

// maxPasswordBytes 是管理员密码输入上限(防止超长输入拖慢 argon2)。
const maxPasswordBytes = 1024

// securityHeaders 是全局安全响应头。
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; " +
		"base-uri 'none'; form-action 'self'",
	"X-Content-Type-Options":  "nosniff",
	"X-Frame-Options":         "DENY",
	"Referrer-Policy":         "no-referrer",
	"Permissions-Policy":      "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-origin",
}

// securityHeadersWithHSTS 设置全局安全响应头,并在 TLS(反代)部署下补发 HSTS。
// HSTS 只在配置了 SecureCookie 时下发,避免本地 HTTP 调试被浏览器强制升级。
func securityHeadersWithHSTS(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		for k, v := range securityHeaders {
			c.Header(k, v)
		}
		if enabled {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// bodyLimitMiddleware 限制请求体大小,防止超大 payload 带来的内存与 CPU 压力。
func bodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}

// apiCacheControlMiddleware 给 API 响应设置 no-store。
func apiCacheControlMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// csrfCheck 校验状态变更请求的 CSRF token。
//
// 除校验 token 外,还校验 Origin(存在时)必须是同源,
// 以覆盖"同站不同子域/相邻端口"这一 SameSite 无法完全隔离的场景。
func csrfCheck(mgr *authManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := sessionIDFromCookie(c)
		if sessionID == "" {
			failCode(c, http.StatusForbidden, "CSRF_INVALID", "缺少会话")
			return
		}
		if !sameOriginRequest(c) {
			failCode(c, http.StatusForbidden, "CSRF_INVALID", "CSRF 校验失败")
			return
		}
		token := c.GetHeader("X-CSRF-Token")
		if token == "" || !mgr.ValidateCSRF(sessionID, token) {
			failCode(c, http.StatusForbidden, "CSRF_INVALID", "CSRF 校验失败")
			return
		}
		c.Next()
	}
}

// sameOriginRequest 在请求带 Origin 时检查其与 Host 是否同源。
// 不带 Origin 的请求(如服务端脚本、curl)依赖 CSRF token 本身保护。
func sameOriginRequest(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, c.Request.Host)
}
