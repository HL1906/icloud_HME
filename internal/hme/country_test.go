package hme

import "testing"

// TestCountryCodeByHost 验证账号区域代码跟随 Host:
// 中国区账号在 accountLogin 时必须报 CHN,否则会拿到错误区域的端点。
func TestCountryCodeByHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"icloud.com", "USA"},
		{"", "USA"},
		{"icloud.com.cn", "CHN"},
	}
	for _, tc := range cases {
		c := &Client{Host: tc.host}
		if got := c.countryCode(); got != tc.want {
			t.Fatalf("host=%q 期望 %q,得到 %q", tc.host, tc.want, got)
		}
	}
}

// TestSetupURLByHost 验证登录/校验端点跟随账号区域。
func TestSetupURLByHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"icloud.com", "https://setup.icloud.com/setup/ws/1"},
		{"icloud.com.cn", "https://setup.icloud.com.cn/setup/ws/1"},
	}
	for _, tc := range cases {
		c := &Client{Host: tc.host}
		if got := c.SetupURL(); got != tc.want {
			t.Fatalf("host=%q 期望 %q,得到 %q", tc.host, tc.want, got)
		}
	}
}
