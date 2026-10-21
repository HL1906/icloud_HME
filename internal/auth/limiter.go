package auth

import (
	"sync"
	"time"
)

// limiterEntry 记录一个 key 的失败次数与窗口起点。
type limiterEntry struct {
	firstFailure time.Time
	failures     int
}

// Limiter 按客户端 IP 的登录失败限流(固定窗口),线程安全。
//
// 每次访问时删除已过窗口的 key,key 数达到上限时淘汰最早窗口,
// 避免伪造来源地址造成内存 DoS。
//
// 重要:反向代理部署时所有请求的对端 IP 相同(如 127.0.0.1),
// 按 IP 熔断会让任何人通过刷错误密码把管理员一起锁在门外。
// 因此这里额外记录"同一 key 的连续失败"与"全局失败速率",
// 并且**成功登录不受 lockout 影响**——密码正确时始终放行。
type Limiter struct {
	mu          sync.Mutex
	now         func() time.Time
	window      time.Duration
	maxFailures int
	maxKeys     int
	failures    map[string]limiterEntry
	// maxGlobalFailures 是窗口内全局允许的失败总数(0 表示不限制)
	maxGlobalFailures int
	// globalStart 是当前全局统计窗口的起点;globalCount 是窗口内失败总数
	globalStart time.Time
	globalCount int
}

// NewLimiter 创建限流器。now 为可注入时钟,window 为固定窗口,
// maxFailures 为窗口内允许的最大失败次数,maxKeys 为 key 数上限。
func NewLimiter(now func() time.Time, window time.Duration, maxFailures, maxKeys int) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		now:               now,
		window:            window,
		maxFailures:       maxFailures,
		maxKeys:           maxKeys,
		failures:          make(map[string]limiterEntry),
		maxGlobalFailures: 0,
	}
}

// WithGlobalLimit 设置窗口内全局失败上限,返回自身便于链式调用。
// 用于挡住跨 IP 的分布式爆破;0 表示不限制。
func (l *Limiter) WithGlobalLimit(max int) *Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxGlobalFailures = max
	return l
}

// Allow 判断该 key 当前是否允许尝试登录,并计入一次失败。
//
// 返回 allowed=false 时,retryAfter 为建议等待时间(不超过窗口)。
//
// 安全语义:这里只做"限制爆破速度",不能作为"密码是否正确"的替代。
// 调用方应在密码校验通过时无视 allowed 直接放行,见 CheckPasswordAllowed 的用法说明。
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// 全局窗口:挡住换 IP 的分布式爆破
	l.rollGlobalLocked(now)
	if l.maxGlobalFailures > 0 && l.globalCount >= l.maxGlobalFailures {
		return false, l.window
	}
	l.globalCount++

	entry, exists := l.failures[key]
	if !exists || now.Sub(entry.firstFailure) >= l.window {
		// 新窗口:重置计数
		l.failures[key] = limiterEntry{firstFailure: now, failures: 1}
		l.gcLocked(now)
		return true, 0
	}
	entry.failures++
	if entry.failures > l.maxFailures {
		l.failures[key] = entry
		retryAfter = l.window - now.Sub(entry.firstFailure)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}
	l.failures[key] = entry
	return true, 0
}

// Blocked 报告该 key 当前是否处于封锁状态(不计数)。
// 用于先判断是否允许尝试,再在失败时调用 Allow 记账。
func (l *Limiter) Blocked(key string) (blocked bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.rollGlobalLocked(now)
	if l.maxGlobalFailures > 0 && l.globalCount >= l.maxGlobalFailures {
		return true, l.window
	}
	entry, exists := l.failures[key]
	if !exists || now.Sub(entry.firstFailure) >= l.window {
		return false, 0
	}
	if entry.failures > l.maxFailures {
		wait := l.window - now.Sub(entry.firstFailure)
		if wait < 0 {
			wait = 0
		}
		return true, wait
	}
	return false, 0
}

// rollGlobalLocked 在窗口滚动时重置全局计数,须持锁调用。
func (l *Limiter) rollGlobalLocked(now time.Time) {
	if l.globalStart.IsZero() || now.Sub(l.globalStart) >= l.window {
		l.globalStart = now
		l.globalCount = 0
	}
}

// Success 标记登录成功,清除该 key 的失败记录。
//
// 注意:成功登录本身不受 lockout 影响(见 Allow),因此这里只做清理。
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// gcLocked 清理过窗 key 并淘汰最早窗口,须持锁调用。
func (l *Limiter) gcLocked(now time.Time) {
	for k, e := range l.failures {
		if now.Sub(e.firstFailure) >= l.window {
			delete(l.failures, k)
		}
	}
	if len(l.failures) > l.maxKeys {
		oldest := time.Time{}
		var oldestKey string
		for k, e := range l.failures {
			if oldest.IsZero() || e.firstFailure.Before(oldest) {
				oldest = e.firstFailure
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(l.failures, oldestKey)
		}
	}
}
