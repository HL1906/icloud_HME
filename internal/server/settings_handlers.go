package server

// 系统设置接口。
//
// 这里只管"运行时可改"的设置;监听地址、数据目录、管理员口令属于部署期配置,
// 仍然走命令行参数/环境变量,改完必须重启,页面上只做只读展示。

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/settings"
)

// envInfo 是设置页要展示的只读部署信息。
type envInfo struct {
	// DataDir 是数据目录。
	DataDir string `json:"data_dir"`
	// PublicBaseURLEnv 是环境变量里的对外域名;为空表示没设。
	PublicBaseURLEnv string `json:"public_base_url_env"`
	// Version 是构建版本。
	Version string `json:"version"`
	// SecureCookie 标记是否按 HTTPS 部署。
	SecureCookie bool `json:"secure_cookie"`
}

// getSettingsHandler 处理 GET /api/settings。
func (s *Server) getSettingsHandler(c *gin.Context) {
	ok(c, gin.H{
		"settings": s.settings.Get(),
		"env": envInfo{
			DataDir:          s.cfg.DataDir,
			PublicBaseURLEnv: strings.TrimSpace(s.cfg.PublicBaseURL),
			Version:          Version,
			SecureCookie:     s.cfg.SecureCookie,
		},
	})
}

// updateSettingsHandler 处理 PUT /api/settings。
//
// 整表单覆盖:前端把当前值全量提交,后端校验后落盘。
// 自动回收的间隔改了要重建 ticker,所以保存成功后顺手重启协程。
func (s *Server) updateSettingsHandler(c *gin.Context) {
	var req settings.Settings
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	saved, err := s.settings.Save(req)
	if err != nil {
		var invalid *settings.InvalidError
		if errors.As(err, &invalid) {
			failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", invalid.Message)
			return
		}
		failCode(c, http.StatusInternalServerError, "STORAGE_FAILURE", "保存失败,请稍后重试")
		return
	}
	// 只在协程已经在跑时重建:测试或 Run 之前不该因为一次保存就启动后台协程
	if s.sweeperRunning() {
		s.restartTokenSweeper()
	}
	ok(c, saved)
}
