package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/aliasmeta"
	"icloud-hme/internal/hme"
	"icloud-hme/internal/pool"
)

// poolAdapter 把 server.Backend 收敛成 pool 需要的最小能力。
//
// 这样可以避免 pool 包反向依赖 server(接口定义在使用方,Go 的惯例)。
type poolAdapter struct {
	be   Backend
	meta *aliasmeta.Store
}

func (a poolAdapter) AccountIDs() []string {
	accounts := a.be.ListAccounts()
	ids := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		ids = append(ids, acc.ID)
	}
	return ids
}

func (a poolAdapter) ListAliases(accountID string) ([]hme.Alias, error) {
	return a.be.ListAliases(accountID)
}

func (a poolAdapter) CreateAlias(accountID, label string) (*hme.CreateResult, error) {
	return a.be.CreateAlias(accountID, label)
}

// AssignGroup 把自动创建的别名登记到本地分组。
func (a poolAdapter) AssignGroup(accountID, email, groupID string) error {
	if a.meta == nil {
		return nil
	}
	_, err := a.meta.Upsert(email, accountID, groupID, "")
	return err
}

// poolUnavailable 统一处理"调度器没启用"的情况。
func (s *Server) poolUnavailable(c *gin.Context) bool {
	if s.pool == nil {
		failCode(c, http.StatusServiceUnavailable, "POOL_UNAVAILABLE", "自动补号功能未启用")
		return true
	}
	return false
}

// listPoolHandler 处理 GET /api/pool。
func (s *Server) listPoolHandler(c *gin.Context) {
	if s.poolUnavailable(c) {
		return
	}
	ok(c, gin.H{"configs": s.pool.List()})
}

// poolConfigReq 是 PUT /api/pool/:id 的请求体。
type poolConfigReq struct {
	Enabled     bool   `json:"enabled"`
	Target      int    `json:"target"`
	CountMode   string `json:"count_mode"`
	MaxPerRun   int    `json:"max_per_run"`
	IntervalSec int    `json:"interval_sec"`
	// GroupID: 自动创建的别名归入哪个本地分组(空 = 未分组)
	GroupID string `json:"group_id"`
}

// updatePoolHandler 处理 PUT /api/pool/:id(新增或更新配置)。
func (s *Server) updatePoolHandler(c *gin.Context) {
	if s.poolUnavailable(c) {
		return
	}
	id := c.Param("id")
	var req poolConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	cfg, err := s.pool.Upsert(pool.Config{
		AccountID:   id,
		Enabled:     req.Enabled,
		Target:      req.Target,
		CountMode:   req.CountMode,
		MaxPerRun:   req.MaxPerRun,
		IntervalSec: req.IntervalSec,
		GroupID:     req.GroupID,
	})
	if err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	ok(c, cfg)
}

// deletePoolHandler 处理 DELETE /api/pool/:id(关闭并移除配置)。
func (s *Server) deletePoolHandler(c *gin.Context) {
	if s.poolUnavailable(c) {
		return
	}
	id := c.Param("id")
	if !s.pool.Remove(id) {
		failCode(c, http.StatusNotFound, "POOL_NOT_CONFIGURED", "该账号未配置自动补号")
		return
	}
	ok(c, gin.H{"account_id": id})
}

// runPoolHandler 处理 POST /api/pool/:id/run。
//
// 异步执行:补齐可能要建好几个别名,每个之间还有间隔,
// 同步等待会顶到 HTTP 写超时,因此只回"已触发",结果由 GET /api/pool 轮询。
func (s *Server) runPoolHandler(c *gin.Context) {
	if s.poolUnavailable(c) {
		return
	}
	id := c.Param("id")
	err := s.pool.Trigger(id)
	switch {
	case err == nil:
		ok(c, gin.H{"account_id": id, "triggered": true})
	case errors.Is(err, pool.ErrNotConfigured):
		failCode(c, http.StatusNotFound, "POOL_NOT_CONFIGURED", "该账号未配置自动补号")
	case errors.Is(err, pool.ErrBusy):
		failCode(c, http.StatusConflict, "POOL_BUSY", "该账号正在检查中,请稍后再试")
	case errors.Is(err, pool.ErrClosed):
		failCode(c, http.StatusServiceUnavailable, "POOL_UNAVAILABLE", "调度器已停止")
	default:
		failCode(c, http.StatusInternalServerError, "POOL_ERROR", err.Error())
	}
}
