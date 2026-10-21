package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// 收信活动扫描的取值边界。窗口太短看不出"久未收信",太长又会拖慢一次扫描。
const (
	defaultActivityDays  = 90
	maxActivityDays      = 365
	defaultActivityLimit = 500
	maxActivityLimit     = 2000
)

// scanActivityReq 是收信活动扫描请求。
type scanActivityReq struct {
	AccountID string `json:"account_id"`
	Days      int    `json:"days"`
	Limit     int    `json:"limit"`
}

// scanAliasActivityHandler 扫描账号收件箱,刷新每个别名的"最近收信时间"。
//
// 名字里带 alias-activity 而不是塞进 /aliases/:id,是因为 /aliases 下已有
// :id 参数节点,再挂静态段容易和服务端的路由匹配冲突。
func (s *Server) scanAliasActivityHandler(c *gin.Context) {
	var req scanActivityReq
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return
	}
	if req.Days == 0 {
		req.Days = defaultActivityDays
	}
	if req.Limit == 0 {
		req.Limit = defaultActivityLimit
	}
	if req.Days < 1 || req.Days > maxActivityDays {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: days 取值 1-365")
		return
	}
	if req.Limit < 1 || req.Limit > maxActivityLimit {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: limit 取值 1-2000")
		return
	}

	// 同一账号并发扫描只会重复占用 IMAP 连接,直接拒绝更省事
	if !s.beginScan(req.AccountID) {
		failCode(c, http.StatusConflict, "ACTIVITY_BUSY", "该账号正在扫描中,请稍后重试")
		return
	}
	defer s.endScan(req.AccountID)

	scan, err := s.be.ScanAliasActivity(req.AccountID, req.Days, req.Limit)
	if err != nil {
		backendFail(c, err)
		return
	}

	at := time.Now()
	updated, err := s.meta.ApplyActivity(req.AccountID, scan.Hits, scan.Scanned, req.Days, at)
	if err != nil {
		// 扫描本身成功了,但结果没保存:如实报错,否则前端会以为数据已就绪
		failCode(c, http.StatusInternalServerError, "STORAGE_FAILURE", "收信时间未能保存,请稍后重试")
		return
	}

	ok(c, gin.H{
		"account_id":   req.AccountID,
		"method":       scan.Method,
		"scanned":      scan.Scanned,
		"matched":      len(scan.Hits),
		"updated":      updated,
		"window_days":  req.Days,
		"last_scan_at": at.Format(time.RFC3339),
	})
}

// aliasActivityHandler 返回账号最近一次扫描的状态,供页面提示"上次扫描时间"。
func (s *Server) aliasActivityHandler(c *gin.Context) {
	accountID := c.Query("account_id")
	if accountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: account_id")
		return
	}
	act, found := s.meta.GetActivity(accountID)
	ok(c, gin.H{
		"account_id": accountID,
		"available":  found,
		"activity":   act,
	})
}

// beginScan 尝试占用账号的扫描名额。已被占用时返回 false。
func (s *Server) beginScan(accountID string) bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.scanning[accountID] {
		return false
	}
	s.scanning[accountID] = true
	return true
}

// endScan 释放账号的扫描名额。
func (s *Server) endScan(accountID string) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	delete(s.scanning, accountID)
}
