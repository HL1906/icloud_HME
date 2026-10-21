package netsafe

import (
	"testing"
)

func TestDefaultPolicyBlocksInternalAddresses(t *testing.T) {
	p := DefaultIMAPPolicy()
	blocked := []string{
		"127.0.0.1",       // 回环
		"::1",             // IPv6 回环
		"10.1.2.3",        // 私有网段
		"192.168.1.10",    // 私有网段
		"172.16.0.1",      // 私有网段
		"169.254.169.254", // 云元数据
		"100.64.0.1",      // CGNAT
		"0.0.0.0",         // 未指定
		"fe80::1",         // 链路本地
		"fc00::1",         // 唯一本地
	}
	for _, host := range blocked {
		if err := p.CheckAddr(host, 993); err == nil {
			t.Errorf("%s 应被拒绝", host)
		}
	}
}

func TestDefaultPolicyBlocksUnexpectedPorts(t *testing.T) {
	p := DefaultIMAPPolicy()
	for _, port := range []int{22, 6379, 3306, 80, 0, 70000} {
		if err := p.CheckAddr("8.8.8.8", port); err == nil {
			t.Errorf("端口 %d 应被拒绝", port)
		}
	}
}

func TestDefaultPolicyAllowsPublicIMAP(t *testing.T) {
	p := DefaultIMAPPolicy()
	for _, port := range []int{993, 143} {
		if err := p.CheckAddr("8.8.8.8", port); err != nil {
			t.Errorf("公网地址 + 端口 %d 应被允许: %v", port, err)
		}
	}
}

func TestAllowPrivateOptIn(t *testing.T) {
	// 本地自建 IMAP 场景可显式放开内网
	p := Policy{AllowPrivate: true}
	if err := p.CheckAddr("127.0.0.1", 993); err != nil {
		t.Fatalf("AllowPrivate 应允许回环地址: %v", err)
	}
}
