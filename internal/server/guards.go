package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/hme"
)

// allowCreate 检查并记录一次建号请求,被限流时写好 429 响应并返回 false。
//
// 密钥用的是"账号"而不是 IP:同一个人可能换 IP 重试,但真正会被
// iCloud 风控的是那个账号本身,所以必须按账号计。
func (s *Server) allowCreate(c *gin.Context, accountID string) bool {
	if s.createLimiter == nil {
		return true
	}
	okNow, retry := s.createLimiter.allow(accountID)
	if okNow {
		return true
	}
	secs := int(retry.Seconds()) + 1
	c.Header("Retry-After", strconv.Itoa(secs))
	failCode(c, http.StatusTooManyRequests, "RATE_LIMITED",
		fmt.Sprintf("建号太频繁了，请 %d 秒后再试（每分钟最多 %d 个）", secs, createRateLimit))
	return false
}

// listAliasesFor 取别名列表:生产后端走 60 秒缓存,其余实现直连。
//
// 总览页会为每个账号各调一次本接口,缓存能把"打开一次页面 = N 次上游请求"
// 压成"一分钟内最多 N 次",既快也少触发 iCloud 的风控。
func (s *Server) listAliasesFor(_ *gin.Context, accountID string) ([]hme.Alias, error) {
	if lister, ok := s.be.(cachedAliasLister); ok {
		return lister.ListAliasesCached(accountID)
	}
	return s.be.ListAliases(accountID)
}

// aliasOwned 校验 alias 属于 accountID,不通过时已写好响应并返回 false。
//
// 查的是该账号的别名列表:本地元数据里有、但账号列表里没有的邮箱,
// 说明已被删掉,同样按"不属于"处理。
func (s *Server) aliasOwned(c *gin.Context, accountID, alias string) bool {
	aliases, err := s.listAliasesFor(c, accountID)
	if err != nil {
		// 取不到列表就不放行:否则等于把校验变成"上游一通就跳过"。
		backendFail(c, err)
		return false
	}
	want := strings.ToLower(strings.TrimSpace(alias))
	for _, a := range aliases {
		if strings.ToLower(strings.TrimSpace(a.Email)) == want {
			return true
		}
	}
	failCode(c, http.StatusNotFound, "ALIAS_NOT_FOUND", "该邮箱不属于这个账号")
	return false
}
