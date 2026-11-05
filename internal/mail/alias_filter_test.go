package mail

import (
	"strings"
	"testing"
)

func TestRecipientMatches(t *testing.T) {
	tests := []struct {
		name  string
		to    string
		alias string
		want  bool
	}{
		{"精确匹配", "a@icloud.com", "a@icloud.com", true},
		{"大小写不敏感", "A@iCloud.com", "a@icloud.com", true},
		{"多收件人之一", "x@example.com, a@icloud.com", "a@icloud.com", true},
		{"带显示名", "Alias <a@icloud.com>", "a@icloud.com", true},
		{"子串不算命中", "aaa@icloud.com", "a@icloud.com", false},
		{"收件人为空", "", "a@icloud.com", false},
		{"别名不相关", "b@icloud.com", "a@icloud.com", false},
		// 回归用例:这正是旧版 IMAP 回退路径用 strings.Contains 会误命中的形状,
		// 一旦按别名对外分发取件链接,误命中就等于泄漏别人的邮件。
		{"后缀相同不算命中", "xabc@icloud.com", "abc@icloud.com", false},
		{"前缀相同不算命中", "abc@icloud.com.evil.example", "abc@icloud.com", false},
		{"显示名里的别名不算命中", "abc@icloud.com <other@example.com>", "abc@icloud.com", false},
		{"多收件人第二段命中", "a@icloud.com, b@icloud.com", "b@icloud.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecipientMatches(tt.to, tt.alias); got != tt.want {
				t.Fatalf("RecipientMatches(%q, %q) = %v, want %v", tt.to, tt.alias, got, tt.want)
			}
		})
	}
}

func TestTruncatePreview(t *testing.T) {
	long := strings.Repeat("好", previewMaxRunes+50)
	got := truncatePreview(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("超长预览应以省略号结尾")
	}
	// 按字符截断,不应截出半个多字节字符
	if runes := []rune(strings.TrimSuffix(got, "…")); len(runes) != previewMaxRunes {
		t.Fatalf("截断后应有 %d 个字符,实际 %d", previewMaxRunes, len(runes))
	}

	short := "验证码 123456"
	if got := truncatePreview(short); got != short {
		t.Fatalf("短预览不应被修改: %q", got)
	}

	if got := truncatePreview("   "); got != "" {
		t.Fatalf("空白预览应为空,实际 %q", got)
	}
}

func TestPreviewFromRawBody(t *testing.T) {
	// 完整的 MIME 片段
	mime := "Content-Type: text/plain\r\n\r\n您的验证码是 654321"
	if got := previewFromRawBody([]byte(mime)); !strings.Contains(got, "654321") {
		t.Fatalf("应从 MIME 片段中提取正文,实际 %q", got)
	}

	// 被截断/非 MIME 的原始片段也要有预览
	if got := previewFromRawBody([]byte("Your code is 111222")); !strings.Contains(got, "111222") {
		t.Fatalf("非 MIME 片段应退化为原文清洗,实际 %q", got)
	}
}

func TestParseUID(t *testing.T) {
	if _, err := ParseUID("123"); err != nil {
		t.Fatalf("数字 UID 应解析成功: %v", err)
	}
	for _, bad := range []string{"", "abc", "0", "-1", "12345678901234567890"} {
		if _, err := ParseUID(bad); err == nil {
			t.Fatalf("ParseUID(%q) 应报错", bad)
		}
	}
}
