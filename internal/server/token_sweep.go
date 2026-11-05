package server

// 取件令牌的自动回收。
//
// 策略按分组下发:分组上设置"签发 N 天后自动回收",后台定时扫一遍到期项
// 并撤销令牌。两条边界是刻意保留的:
//   - 未归入任何分组的别名永远不会被自动回收,一次误设不会波及全部;
//   - 策略为 0 表示不自动回收,这是默认值。
//
// 起算点是 IssuedAt(当前这条令牌的签发时间),不是别名的创建时间:
// 一个号可能建了三个月才发出去,生命周期应当从发出去那天算。

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/settings"
)

// tokenSweepInterval 是自动回收的默认检查周期;实际值来自设置页,可随时改。
const tokenSweepInterval = 10 * time.Minute

// sweepExpiredTokens 执行一轮回收,返回本轮撤销的链接数量。
//
// 逐条撤销而不是整批落盘:RevokeToken 自带回滚与持久化,
// 中途失败最多是"这条下轮再试",不会出现半批写坏文件的情况。
func (s *Server) sweepExpiredTokens(now time.Time) int {
	if !s.settings.Get().AutoRevokeEnabled {
		return 0
	}
	expired := s.meta.ExpiredTokens(now)
	if len(expired) == 0 {
		return 0
	}
	revoked := 0
	for _, entry := range expired {
		if _, err := s.meta.RevokeToken(entry.Email); err != nil {
			log.Printf("[sweep] 回收失败 email=%s: %v", entry.Email, err)
			continue
		}
		revoked++
	}
	if revoked > 0 {
		log.Printf("[sweep] 自动回收 %d 个到期取件链接", revoked)
	}
	return revoked
}

// sweepInterval 返回设置里的扫描间隔。
func (s *Server) sweepInterval() time.Duration {
	minutes := s.settings.Get().AutoRevokeInterval
	if minutes < settings.MinAutoRevokeInterval || minutes > settings.MaxAutoRevokeInterval {
		return tokenSweepInterval
	}
	return time.Duration(minutes) * time.Minute
}

// startTokenSweeper 启动后台回收协程。重复调用无副作用。
func (s *Server) startTokenSweeper() {
	s.sweepMu.Lock()
	if s.sweepStop != nil {
		s.sweepMu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.sweepStop = stop
	s.sweepMu.Unlock()

	go func() {
		// 启动先跑一轮:进程可能停了几天,期间到期的链接应当立刻失效,
		// 而不是等满一个周期。
		s.sweepExpiredTokens(time.Now())
		ticker := time.NewTicker(s.sweepInterval())
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.sweepExpiredTokens(time.Now())
			}
		}
	}()
}

// stopTokenSweeper 停止后台回收协程(供优雅关闭使用)。未启动过则什么都不做。
func (s *Server) stopTokenSweeper() {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if s.sweepStop == nil {
		return
	}
	close(s.sweepStop)
	s.sweepStop = nil
}

// restartTokenSweeper 按最新设置重启回收协程。
//
// 改间隔必须重建 ticker(Go 的 Ticker 不支持改周期),所以这里直接停掉再起;
// 停掉后协程退出前不会再有动作,重建时又会先跑一轮,不存在"改了没生效"的窗口。
func (s *Server) restartTokenSweeper() {
	s.stopTokenSweeper()
	s.startTokenSweeper()
}

// sweeperRunning 报告后台回收协程是否在跑。
func (s *Server) sweeperRunning() bool {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return s.sweepStop != nil
}

// sweepTokensHandler 处理 POST /api/alias-token/sweep。
//
// 后台每 10 分钟会自动跑一次,这个接口用于"刚改完分组策略,想立刻生效"。
func (s *Server) sweepTokensHandler(c *gin.Context) {
	now := time.Now()
	revoked := s.sweepExpiredTokens(now)
	ok(c, gin.H{
		"revoked":    revoked,
		"checked_at": now.Format(time.RFC3339),
	})
}
