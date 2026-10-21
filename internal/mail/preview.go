package mail

import (
	stdhtml "html"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	htmlTagRE        = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlNoiseBlockRE = regexp.MustCompile(`(?is)<(style|script|head|title|noscript)\b[^>]*>.*?</(style|script|head|title|noscript)\s*>`)
	htmlCommentRE    = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBreakRE      = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlBlockEndRE   = regexp.MustCompile(`(?i)</(p|div|tr|h[1-6])\s*>`)
	htmlListItemRE   = regexp.MustCompile(`(?i)<li\b[^>]*>`)

	cssSignalRE      = regexp.MustCompile(`(?i)(@font-face|@media|@import|@supports|@keyframes|(^|[\s;{])(-webkit-|-moz-|-ms-|mso-)[\w-]*|(^|[\s;{])(font-family|text-size-adjust|border-collapse|mso-table-[\w-]+)\s*:)`)
	cssDeclarationRE = regexp.MustCompile(`(?i)(^|[;{])\s*[-a-z_][\w-]*\s*:\s*[^;{}]+`)
)

// maxPreviewInputBytes 限制参与预览清洗的输入长度。
// 预览只需要开头一段文本,超长正文(如 5MB 的 HTML 邮件)没必要整体扫描。
const maxPreviewInputBytes = 256 * 1024

// sanitizePreview converts an email preview to readable text and removes
// presentation-only HTML/CSS that mail clients commonly include.
func sanitizePreview(raw string) string {
	if len(raw) > maxPreviewInputBytes {
		// 在 UTF-8 边界上截断,避免切出半个多字节字符
		cut := maxPreviewInputBytes
		for cut > 0 && !utf8.RuneStart(raw[cut]) {
			cut--
		}
		raw = raw[:cut]
	}
	text := stripHTML(raw)
	return sanitizePlainPreview(text)
}

func sanitizePlainPreview(raw string) string {
	text := normalizePreview(raw)
	if text == "" || !looksLikeCSS(text) {
		return text
	}

	// Some upstream APIs return the contents of a <style> block without the
	// surrounding tags. Remove balanced CSS rules while preserving any text
	// that follows the stylesheet.
	text = normalizePreview(stripCSSRules(text))
	if text == "" || looksLikeCSS(text) {
		return ""
	}
	return text
}

// stripHTML removes tags and invisible document sections while keeping common
// line breaks so the preview remains readable.
func stripHTML(raw string) string {
	raw = htmlNoiseBlockRE.ReplaceAllString(raw, "")
	raw = htmlCommentRE.ReplaceAllString(raw, "")
	raw = htmlBreakRE.ReplaceAllString(raw, "\n")
	raw = htmlBlockEndRE.ReplaceAllString(raw, "\n")
	raw = htmlListItemRE.ReplaceAllString(raw, "\n- ")
	raw = htmlTagRE.ReplaceAllString(raw, "")
	return normalizePreview(stdhtml.UnescapeString(raw))
}

func normalizePreview(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func looksLikeCSS(text string) bool {
	braces := strings.Count(text, "{")
	semicolons := strings.Count(text, ";")
	signals := len(cssSignalRE.FindAllStringIndex(text, -1))
	declarations := len(cssDeclarationRE.FindAllStringIndex(text, -1))
	trimmed := strings.TrimSpace(text)
	selectorStart := strings.HasPrefix(trimmed, ".") ||
		strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "@")

	if braces == 0 {
		return signals >= 2 && semicolons >= 3
	}
	if selectorStart && declarations >= 1 {
		return true
	}
	return (signals >= 2 && semicolons >= 2) ||
		(braces >= 3 && declarations >= 3 && semicolons >= 3)
}

// stripCSSRules 移除平衡的 CSS 规则块,保留规则之外的文本。
//
// 实现为一趟扫描:先用单个游标记录"保留区间",最后一次性拼接。
// 旧实现每移除一条规则就对整串重新切片拼接(并从头部重扫),
// 在大量 `{...}` 的正文上会退化为 O(n²)。
func stripCSSRules(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	kept := 0 // 已保留内容的结束位置
	i := 0
	for i < len(text) {
		open := strings.IndexByte(text[i:], '{')
		if open < 0 {
			break
		}
		open += i
		close := matchingBrace(text, open)
		if close < 0 {
			// 花括号不平衡:剩余内容原样保留
			break
		}
		// 规则起点回溯到行首
		start := open
		for start > kept {
			if text[start-1] == '}' || text[start-1] == '\n' || text[start-1] == '\r' {
				break
			}
			start--
		}
		b.WriteString(text[kept:start])
		i = close + 1
		kept = i
	}
	b.WriteString(text[kept:])
	return b.String()
}

func matchingBrace(text string, open int) int {
	depth := 0
	var quote byte
	escaped := false
	for i := open; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
