package server

import (
	"sync"
	"time"
)

// createRateLimit 是"每账号每分钟最多建几个号"的上限。
//
// 这不是防坏人(建号接口本来就要管理员会话),而是保护上游:
// iCloud 对短时间大量创建 HME 别名非常敏感,手滑连点、脚本失控
// 都可能让主号被风控。
//
// 从 10 降到 4:真人即便批量建号,节奏也到不了每分钟 10 个,
// 而"分钟级密集建号"正是 Apple 侧最容易识别的批量特征。补号单轮
// 上限是 3 个(DefaultMaxPerRun),4 不会卡住正常补号。
const createRateLimit = 4

// createRateWindow 是限流的时间窗口。
const createRateWindow = time.Minute

// createLimiter 按账号做滑动窗口限流。
//
// 与登录限流器(auth.Limiter)分开,是因为两者的键和窗口语义不同:
// 登录按 IP,建号按账号,而且建号成功也要计入(成功调用才消耗上游配额)。
type createLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

func newCreateLimiter() *createLimiter {
	return &createLimiter{
		hits:   make(map[string][]time.Time),
		limit:  createRateLimit,
		window: createRateWindow,
		now:    time.Now,
	}
}

// allow 判断该账号现在能不能再建一个号。
//
// 返回 false 时同时给出"还要等多久",写进 Retry-After 让调用方知道该退避多久。
// 允许的调用会立即计入,不等创建成功 —— 否则失败重试同样会打爆上游。
func (l *createLimiter) allow(accountID string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	// 顺手清掉窗口外的记录,避免 map 无限增长
	prev := l.hits[accountID]
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= l.limit {
		// 最早那次记录滑出窗口时才能再建
		retry := kept[0].Add(l.window).Sub(now)
		if retry < 0 {
			retry = 0
		}
		l.hits[accountID] = kept
		return false, retry
	}

	kept = append(kept, now)
	l.hits[accountID] = kept
	return true, 0
}

// reset 清空某个账号的记录(主要用于测试)。
func (l *createLimiter) reset(accountID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, accountID)
}
