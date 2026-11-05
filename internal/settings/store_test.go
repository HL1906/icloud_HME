package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultsWithoutFile 没有数据目录时用默认值,且不落盘。
func TestDefaultsWithoutFile(t *testing.T) {
	s, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	def := Defaults()
	if got != def {
		t.Fatalf("应返回默认设置, 实际 %+v", got)
	}
	if !got.AutoRevokeEnabled {
		t.Fatal("默认应开启自动回收")
	}
	if _, err := s.Save(Settings{AutoRevokeInterval: 30, PublicMailHours: 12, PublicMailLimit: 5}); err != nil {
		t.Fatal(err)
	}
	if s.Get().AutoRevokeInterval != 30 {
		t.Fatal("内存模式下保存应生效")
	}
}

// TestSaveAndReload 验证落盘后能读回。
func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		AutoRevokeEnabled:  false,
		AutoRevokeInterval: 45,
		PublicMailHours:    6,
		PublicMailLimit:    3,
		PublicBaseURL:      "https://mail.example.com/",
	}
	saved, err := s.Save(want)
	if err != nil {
		t.Fatal(err)
	}
	// 保存时会去掉结尾斜杠
	if saved.PublicBaseURL != "https://mail.example.com" {
		t.Fatalf("应去掉结尾斜杠, 实际 %q", saved.PublicBaseURL)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Get()
	if got != saved {
		t.Fatalf("读回不一致:\nwant %+v\ngot  %+v", saved, got)
	}
}

// TestSaveValidation 覆盖各类越界与非法地址。
func TestSaveValidation(t *testing.T) {
	s, _ := NewStore("")
	base := Defaults()

	cases := []struct {
		name  string
		patch func(*Settings)
		field string
	}{
		{"间隔为 0", func(c *Settings) { c.AutoRevokeInterval = 0 }, "auto_revoke_interval_min"},
		{"间隔超上限", func(c *Settings) { c.AutoRevokeInterval = 5000 }, "auto_revoke_interval_min"},
		{"小时数为 0", func(c *Settings) { c.PublicMailHours = 0 }, "public_mail_hours"},
		{"小时数超上限", func(c *Settings) { c.PublicMailHours = 999 }, "public_mail_hours"},
		{"条数为 0", func(c *Settings) { c.PublicMailLimit = 0 }, "public_mail_limit"},
		{"条数超上限", func(c *Settings) { c.PublicMailLimit = 500 }, "public_mail_limit"},
		{"域名缺协议", func(c *Settings) { c.PublicBaseURL = "mail.example.com" }, "public_base_url"},
		{"域名协议不对", func(c *Settings) { c.PublicBaseURL = "ftp://mail.example.com" }, "public_base_url"},
		{"域名带查询串", func(c *Settings) { c.PublicBaseURL = "https://m.example.com/?a=1" }, "public_base_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.patch(&next)
			_, err := s.Save(next)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("应返回 ErrInvalid, 实际 %v", err)
			}
			var inv *InvalidError
			if !errors.As(err, &inv) || inv.Field != tc.field {
				t.Fatalf("字段名应为 %q, 实际 %+v", tc.field, err)
			}
		})
	}

	// 合法的空域名表示"按请求推导"
	next := base
	next.PublicBaseURL = ""
	if _, err := s.Save(next); err != nil {
		t.Fatalf("空域名应合法: %v", err)
	}
}

// TestLoadNormalizesGarbage 手工改坏的文件不能让服务起不来。
func TestLoadNormalizesGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)

	// 越界值 + 结尾斜杠
	if err := os.WriteFile(path, []byte(`{
  "auto_revoke_enabled": true,
  "auto_revoke_interval_min": 0,
  "public_mail_hours": 9999,
  "public_mail_limit": -3,
  "public_base_url": "https://mail.example.com/"
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("坏值不应导致初始化失败: %v", err)
	}
	got := s.Get()
	if got.AutoRevokeInterval != Defaults().AutoRevokeInterval {
		t.Fatalf("越界间隔应回落默认值, 实际 %d", got.AutoRevokeInterval)
	}
	if got.PublicMailHours != Defaults().PublicMailHours || got.PublicMailLimit != Defaults().PublicMailLimit {
		t.Fatalf("越界取件页参数应回落默认值, 实际 %+v", got)
	}
	if got.PublicBaseURL != "https://mail.example.com" {
		t.Fatalf("应去掉结尾斜杠, 实际 %q", got.PublicBaseURL)
	}

	// 完全损坏的 JSON 同样不能抛错
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("损坏文件不应导致初始化失败: %v", err)
	}
	if s2.Get() != Defaults() {
		t.Fatalf("损坏文件应回落到默认值, 实际 %+v", s2.Get())
	}
}
