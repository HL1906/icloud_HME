package mail

import "testing"

func TestSplitAddresses(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"单个地址", "a@icloud.com", []string{"a@icloud.com"}},
		{"带显示名", "Shop <shop@x.com>", []string{"shop@x.com"}},
		{"多个地址", "a@x.com, b@y.com", []string{"a@x.com", "b@y.com"}},
		{"分号分隔", "a@x.com; b@y.com", []string{"a@x.com", "b@y.com"}},
		{"空值", "   ", nil},
		{"夹杂非地址", "not-an-address, c@z.com", []string{"c@z.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitAddresses(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitAddresses(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("第 %d 个地址 = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestScanRecipientsRequiresConnection(t *testing.T) {
	c := NewClient("user@icloud.com", "app-password")
	if _, err := c.ScanRecipients(10, 90); err == nil {
		t.Fatal("未连接时应报错")
	}
}
