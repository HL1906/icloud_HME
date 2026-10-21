package mail

import (
	"net"
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/netsafe"
)

// TestGuardBlocksLoopback 验证带地址防护的客户端不会连接回环地址(SSRF 回归用例)。
func TestGuardBlocksLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("无法监听本地端口: %v", err)
	}
	defer ln.Close()

	// 若发生连接则记录(用于证明"确实被拦住了",而不是碰巧没连)
	accepted := make(chan struct{}, 1)
	go func() {
		if conn, aerr := ln.Accept(); aerr == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	policy := netsafe.Policy{AllowedPorts: map[int]bool{port: true}} // 端口放行,只靠地址拦截
	c := NewClientWithServer("user@example.com", "secret", "127.0.0.1", port).WithGuard(policy)

	err = c.Connect()
	if err == nil {
		t.Fatal("连接回环地址本应被拒绝")
	}
	if !strings.Contains(err.Error(), "不允许连接内网") && !strings.Contains(err.Error(), "保留地址") {
		t.Fatalf("错误应说明地址被限制,实际: %v", err)
	}

	select {
	case <-accepted:
		t.Fatal("防护失效: 请求打到了回环地址上的服务")
	case <-time.After(300 * time.Millisecond):
		// 未建立连接,符合预期
	}
}

// TestGuardBlocksMetadataEndpoint 验证云元数据地址被拦截。
func TestGuardBlocksMetadataEndpoint(t *testing.T) {
	policy := netsafe.DefaultIMAPPolicy()
	for _, host := range []string{"169.254.169.254", "127.0.0.1", "10.0.0.5", "192.168.1.1", "::1"} {
		// 用 993 端口,确保是地址而非端口导致的拒绝
		if err := policy.CheckAddr(host, 993); err == nil {
			t.Fatalf("%s 应被地址策略拒绝", host)
		}
	}
}

// TestGuardAllowsPublicHost 验证公网地址不受误伤。
func TestGuardAllowsPublicHost(t *testing.T) {
	if err := netsafe.DefaultIMAPPolicy().CheckAddr("8.8.8.8", 993); err != nil {
		t.Fatalf("公网地址不应被拒绝: %v", err)
	}
}
