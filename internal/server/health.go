package server

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

// startedAt 记录进程启动时间,用于 healthz 报告运行时长。
// 放在包级而不是 Server 字段上,是因为同进程内只会有一个 Server。
var startedAt = time.Now()

// handleHealthz 处理 GET /api/healthz(公开,无需登录)。
//
// 只回答"这个进程还能不能干活",不返回版本号细节、账号数量等任何
// 能被用来侦察的信息 —— 它是给 systemd / Docker / 反代探活用的。
//
// 数据目录不可写时返回 503:那种状态下写别名元数据、转发标记都会失败,
// 属于"活着但坏了",探活工具应当能看出来。
func (s *Server) handleHealthz(c *gin.Context) {
	dir := s.cfg.DataDir
	if dir == "" {
		dir = "."
	}
	body := gin.H{
		"status":         "ok",
		"uptime_seconds": int64(time.Since(startedAt).Seconds()),
	}
	if err := checkWritable(dir); err != nil {
		body["status"] = "degraded"
		body["reason"] = "数据目录不可写"
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, body)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, body)
}

// checkWritable 通过真的写一个临时文件来确认目录可写。
//
// 只看 os.Stat 不够:只读挂载、磁盘满、权限被回收都可能是"目录存在但写不进去"。
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".healthz-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	// 清理失败不影响判定:目录毕竟写成功了
	_ = os.Remove(filepath.Clean(name))
	return nil
}
