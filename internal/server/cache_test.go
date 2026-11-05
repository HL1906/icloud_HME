package server

import (
	"sync"
	"testing"
	"time"

	"icloud-hme/internal/hme"
)

// newCountingBackend 造一个 managerBackend,上游取数换成计数器。
func newCountingBackend() (*managerBackend, *int, *sync.Mutex) {
	var mu sync.Mutex
	calls := 0
	b := &managerBackend{
		aliasCache: map[string]aliasCacheEntry{},
		listAliasesFn: func(string) ([]hme.Alias, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return []hme.Alias{{Email: "a@icloud.com", Active: true}}, nil
		},
	}
	return b, &calls, &mu
}

func readCount(calls *int, mu *sync.Mutex) int {
	mu.Lock()
	defer mu.Unlock()
	return *calls
}

// TestCachedAliasesReuseWithinTTL TTL 内重复查询不重复打上游。
//
// 这是"打开总览页 = N 次上游请求"的直接对策:账号列表里每个账号各查一次,
// 没有缓存时每刷新一次页面就把 N 个账号全部重新问一遍。
func TestCachedAliasesReuseWithinTTL(t *testing.T) {
	b, calls, mu := newCountingBackend()

	for i := 0; i < 5; i++ {
		aliases, err := b.ListAliasesCached("acc_1")
		if err != nil {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
		if len(aliases) != 1 {
			t.Fatalf("应返回 1 个别名, 实际 %d", len(aliases))
		}
	}
	if got := readCount(calls, mu); got != 1 {
		t.Fatalf("TTL 内 5 次查询应只打 1 次上游, 实际 %d", got)
	}
}

// TestCachedAliasesPerAccount 不同账号各有独立缓存。
func TestCachedAliasesPerAccount(t *testing.T) {
	b, calls, mu := newCountingBackend()

	_, _ = b.ListAliasesCached("acc_1")
	_, _ = b.ListAliasesCached("acc_2")
	_, _ = b.ListAliasesCached("acc_1")

	if got := readCount(calls, mu); got != 2 {
		t.Fatalf("两个账号各取一次,应为 2 次, 实际 %d", got)
	}
}

// TestCachedAliasesExpires TTL 过后重新取数。
func TestCachedAliasesExpires(t *testing.T) {
	b, calls, mu := newCountingBackend()

	if _, err := b.ListAliasesCached("acc_1"); err != nil {
		t.Fatal(err)
	}
	// 把缓存时间往前推,模拟过期
	b.aliasMu.Lock()
	entry := b.aliasCache["acc_1"]
	entry.at = time.Now().Add(-2 * aliasCacheTTL)
	b.aliasCache["acc_1"] = entry
	b.aliasMu.Unlock()

	if _, err := b.ListAliasesCached("acc_1"); err != nil {
		t.Fatal(err)
	}
	if got := readCount(calls, mu); got != 2 {
		t.Fatalf("过期后应重新取数,共 2 次, 实际 %d", got)
	}
}

// TestInvalidateAliasCache 主动失效后立刻重新取数。
//
// 建号/删除/启停都会调用它:不失效的话页面会继续显示旧的 Active 状态。
func TestInvalidateAliasCache(t *testing.T) {
	b, calls, mu := newCountingBackend()

	_, _ = b.ListAliasesCached("acc_1")
	b.invalidateAliasCache("acc_1")
	_, _ = b.ListAliasesCached("acc_1")

	if got := readCount(calls, mu); got != 2 {
		t.Fatalf("失效后应重新取数, 实际 %d 次", got)
	}
}

// TestCachedAliasesDoesNotCacheError 上游报错不该被缓存住。
func TestCachedAliasesDoesNotCacheError(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	shouldFail := true
	b := &managerBackend{
		aliasCache: map[string]aliasCacheEntry{},
		listAliasesFn: func(string) ([]hme.Alias, error) {
			mu.Lock()
			calls++
			fail := shouldFail
			mu.Unlock()
			if fail {
				return nil, &BackendError{Status: 502, Code: "UPSTREAM_FAILURE", Message: "上游挂了"}
			}
			return []hme.Alias{{Email: "a@icloud.com"}}, nil
		},
	}

	if _, err := b.ListAliasesCached("acc_1"); err == nil {
		t.Fatal("第一次应报错")
	}
	mu.Lock()
	shouldFail = false
	mu.Unlock()

	aliases, err := b.ListAliasesCached("acc_1")
	if err != nil {
		t.Fatalf("上游恢复后应成功, 实际 %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("应返回 1 个, 实际 %d", len(aliases))
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 2 {
		t.Fatalf("错误不该被缓存,应重试, 实际调用 %d 次", n)
	}
}
