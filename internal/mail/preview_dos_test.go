package mail

import (
	"strings"
	"testing"
	"time"
)

// TestPreviewPathologicalInputs 用构造的恶意正文验证预览处理不会退化。
// 这些输入用于回归 ReDoS / O(n^2) 类问题。
func TestPreviewPathologicalInputs(t *testing.T) {
	if raceEnabled() {
		t.Skip("race 模式下放宽时间限制")
	}
	cases := []struct {
		name  string
		input string
	}{
		{"大量未闭合 style 标签", strings.Repeat("<style", 20000)},
		{"大量注释开头", strings.Repeat("<!--", 20000)},
		{"大量尖括号", strings.Repeat("<", 100000)},
		{"大量未闭合 style + 结尾闭合", strings.Repeat("<style", 20000) + strings.Repeat("</style>", 100)},
		{"大量 CGI 风格 CSS 规则", strings.Repeat(".a{color:red;}", 20000)},
		{"嵌套花括号", strings.Repeat("{", 20000) + strings.Repeat("}", 20000)},
		// 接近 1MB 的极端输入:验证即使没有长度上限也不会失控
		{"1MB 未闭合 style", strings.Repeat("<style", 170000)},
		{"1MB CSS 规则", strings.Repeat(".a{color:red;}", 70000)},
		{"1MB 注释", strings.Repeat("<!--", 250000)},
	}

	const budget = 1500 * time.Millisecond
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			_ = sanitizePreview(tc.input)
			if elapsed := time.Since(start); elapsed > budget {
				t.Fatalf("处理 %d 字节耗时 %v，超过预算 %v（疑似灾难性回溯或平方级算法）",
					len(tc.input), elapsed, budget)
			}
		})
	}
}
