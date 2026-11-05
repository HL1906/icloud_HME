// Package server - 分发凭证(取件令牌)的管理接口。
//
// 管理员侧只有三个动作:
//   - 签发:POST   /api/alias-token  → 拿到 "邮箱:令牌" 和完整取件链接
//   - 撤销:DELETE /api/alias-token  → 链接立即失效,别名回到库存
//   - 批量:POST   /api/alias-bulk  action=issue_token
//
// 令牌是纯 bearer 凭证,拿到就能读那一个别名的邮件,所以这里不做
// "只显示一次"的处理:管理员必须能随时把已经发出去的链接再抄一遍。
package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/aliasmeta"
)

type aliasTokenReq struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	// Rotate 为 true 时强制换新令牌(旧链接立即失效)。
	Rotate bool `json:"rotate"`
	// Recipient 是可选的"发给谁",只存在本地。
	Recipient string `json:"recipient"`
}

// issueAliasTokenHandler 处理 POST /api/alias-token。
func (s *Server) issueAliasTokenHandler(c *gin.Context) {
	var req aliasTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if accountID == "" || email == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 与 email 必填")
		return
	}
	if len(email) > 320 || !strings.Contains(email, "@") {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 邮箱地址不合法")
		return
	}
	if !s.accountExists(accountID) {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}

	entry, err := s.meta.IssueToken(email, accountID, req.Recipient, req.Rotate)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, s.aliasTokenPayload(c, entry))
}

// revokeAliasTokenHandler 处理 DELETE /api/alias-token?email=。
func (s *Server) revokeAliasTokenHandler(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.Query("email")))
	if email == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: email")
		return
	}
	entry, err := s.meta.RevokeToken(email)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, gin.H{"email": entry.Email, "state": entry.State, "revoked": true})
}

// aliasTokenPayload 组装签发响应:令牌 + 可以直接发出去的两条链接。
//
// 同时给出 html(给人点开)与 json(给脚本对接)两种形态,
// 以及 wdmail 风格的 "邮箱----链接" 分发串,省得管理员自己拼。
func (s *Server) aliasTokenPayload(c *gin.Context, entry aliasmeta.Entry) gin.H {
	base := s.publicBaseURL(c)
	link := publicAliasURL(base, entry.Email, entry.Token)
	jsonLink := strings.Replace(link, "f=html", "f=json", 1)
	return gin.H{
		"email":       entry.Email,
		"account_id":  entry.AccountID,
		"token":       entry.Token,
		"state":       entry.State,
		"issued_at":   entry.IssuedAt,
		"recipient":   entry.Recipient,
		"url":         link,
		"url_json":    jsonLink,
		"distribute":  entry.Email + "----" + link,
		"public_base": base,
	}
}

// accountExists 判断账号是否还存在(避免给已删除账号签发死链接)。
func (s *Server) accountExists(accountID string) bool {
	for _, acc := range s.be.ListAccounts() {
		if acc.ID == accountID {
			return true
		}
	}
	return false
}
