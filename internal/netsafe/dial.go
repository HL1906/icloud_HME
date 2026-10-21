// Package netsafe 提供出站连接的目标地址限制,用于防止 SSRF。
//
// 背景:本项目允许管理员为账号配置「外部收件邮箱」的 IMAP 服务器地址。
// 若只做字符串校验(比如禁止 "://"),攻击者仍可让服务去连接 127.0.0.1、
// 内网网段或云元数据地址(169.254.169.254),把本服务当成内网探测器使用。
//
// 因此限制必须做在**拨号层**:在真正建立 TCP 连接之前校验解析后的 IP。
// 这样也能覆盖 DNS rebinding——域名在校验时解析出的地址与连接时相同。
package netsafe

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Policy 描述允许建立的出站连接范围。
type Policy struct {
	// AllowPrivate 为 true 时允许连接内网/回环地址(本地开发自建 IMAP 时用)。
	// 默认 false:只允许连接公网地址。
	AllowPrivate bool
	// AllowedPorts 非空时只允许这些端口;空表示不限制端口。
	AllowedPorts map[int]bool
}

// DefaultIMAPPolicy 是外部邮箱 IMAP 连接的默认策略:
// 禁止内网/回环/链路本地地址,端口限制为常见 IMAP 端口。
func DefaultIMAPPolicy() Policy {
	return Policy{
		AllowedPorts: map[int]bool{993: true, 143: true, 1993: true, 2993: true},
	}
}

// CheckAddr 校验 host:port 是否允许连接(会做 DNS 解析)。
func (p Policy) CheckAddr(host string, port int) error {
	if err := p.checkPort(port); err != nil {
		return err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("主机不能为空")
	}
	// 是 IP 字面量时直接校验,避免解析误差
	if ip := net.ParseIP(host); ip != nil {
		return p.checkIP(ip)
	}
	// 域名:解析后逐个校验所有地址,任一命中受限区段即拒绝
	// (攻击者可让域名解析出多个地址,只要有一个能连上就有风险)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("无法解析主机名: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("无法解析主机名")
	}
	for _, addr := range ips {
		if err := p.checkIP(addr.IP); err != nil {
			return err
		}
	}
	return nil
}

func (p Policy) checkPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口无效")
	}
	if len(p.AllowedPorts) > 0 && !p.AllowedPorts[port] {
		return fmt.Errorf("端口 %d 不被允许", port)
	}
	return nil
}

// checkIP 判断 IP 是否落在禁止连接的区段。
func (p Policy) checkIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("地址无效")
	}
	if p.AllowPrivate {
		return nil
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("不允许连接内网或保留地址")
	}
	// 云厂商元数据地址与其它特殊网段
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 0: // 0.0.0.0/8
			return fmt.Errorf("不允许连接内网或保留地址")
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127: // CGNAT 100.64/10
			return fmt.Errorf("不允许连接内网或保留地址")
		case ip4[0] == 169 && ip4[1] == 254: // 链路本地/云元数据
			return fmt.Errorf("不允许连接内网或保留地址")
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0: // 192.0.0/24
			return fmt.Errorf("不允许连接内网或保留地址")
		case ip4[0] >= 240: // 保留
			return fmt.Errorf("不允许连接内网或保留地址")
		}
		return nil
	}
	// IPv6:ULA fc00::/7 与 NAT64 映射等
	if len(ip) == net.IPv6len {
		if ip[0]&0xfe == 0xfc {
			return fmt.Errorf("不允许连接内网或保留地址")
		}
	}
	return nil
}

// GuardedDialer 是带地址校验的 TCP 拨号器,可注入到 IMAP 客户端。
type GuardedDialer struct {
	Policy Policy
	Dialer net.Dialer
}

// DialContext 先校验目标地址,再建立连接。
func (d GuardedDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("目标地址格式无效: %w", err)
	}
	port := 0
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return nil, fmt.Errorf("目标端口无效")
	}
	if err := d.Policy.CheckAddr(host, port); err != nil {
		return nil, err
	}
	return d.Dialer.DialContext(ctx, network, addr)
}
