package hme

import (
	"strings"
	"testing"
)

// TestPrepareCookiesQuotesValues 验证 Cookie 头里的值统一带双引号
// (iCloud 严格匹配浏览器格式,不带引号会 401)。
func TestPrepareCookiesQuotesValues(t *testing.T) {
	c, err := NewClient(map[string]string{"a": "1", "b": `"2"`}, "icloud.com", "", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	header := c.prepareCookies("https://www.icloud.com/test")
	if !strings.Contains(header, `a="1"`) {
		t.Fatalf("未加引号: %q", header)
	}
	// 已带引号的值不应变成双层引号
	if !strings.Contains(header, `b="2"`) || strings.Contains(header, `""2""`) {
		t.Fatalf("引号处理错误: %q", header)
	}
}

// TestPrepareCookiesEmpty 验证无 Cookie 时返回空串(调用方据此跳过设置请求头)。
func TestPrepareCookiesEmpty(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.prepareCookies("https://www.icloud.com/"); got != "" {
		t.Fatalf("无 Cookie 时应返回空串, got=%q", got)
	}
}

// TestCookieSnapshotIsolation 验证快照是副本,外部修改不影响客户端内部状态。
func TestCookieSnapshotIsolation(t *testing.T) {
	c, err := NewClient(map[string]string{"sid": "v1"}, "icloud.com", "", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	snap := c.CookieSnapshot()
	snap["sid"] = "tampered"
	snap["extra"] = "x"

	fresh := c.CookieSnapshot()
	if fresh["sid"] != "v1" {
		t.Fatalf("快照被篡改影响内部状态: %v", fresh)
	}
	if _, ok := fresh["extra"]; ok {
		t.Fatalf("快照新增键不应回写内部状态: %v", fresh)
	}

	// NewClient 也不应与传入 map 共享底层存储
	input := map[string]string{"k": "v"}
	c2, _ := NewClient(input, "icloud.com", "", false)
	input["k"] = "changed"
	if got := c2.CookieSnapshot()["k"]; got != "v" {
		t.Fatalf("客户端应持有 Cookie 副本, got=%q", got)
	}
}

// TestSetCookie 验证响应刷新的 Cookie 会被记录且可被读到。
func TestSetCookie(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.setCookie("X-APPLE-WEBAUTH-TOKEN", "new-token")
	if got := c.CookieSnapshot()["X-APPLE-WEBAUTH-TOKEN"]; got != "new-token" {
		t.Fatalf("Cookie 未写入: %q", got)
	}
}
