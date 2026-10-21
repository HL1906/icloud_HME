package mail

import (
	"encoding/base64"
	"net/mail"
	"strings"
	"testing"
)

// buildMessage 把原始 MIME 文本解析为 *mail.Message。
func buildMessage(t *testing.T, raw string) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("构造邮件失败: %v", err)
	}
	return msg
}

// TestReadBodyMultipartBase64 验证 multipart/alternative + base64 正文
// 能取出 text/plain 的真实内容(旧实现会返回 MIME 源码)。
func TestReadBodyMultipartBase64(t *testing.T) {
	plain := "验证码：123456\n15 分钟内有效。"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	raw := "MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/alternative; boundary="BOUND"` + "\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		encoded + "\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>HTML 版本</p></body></html>\r\n" +
		"--BOUND--\r\n"

	got, err := readBody(buildMessage(t, raw))
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if !strings.Contains(got, "123456") {
		t.Fatalf("未提取到验证码, got=%q", got)
	}
	if strings.Contains(got, "BOUND") || strings.Contains(got, "Content-Type") {
		t.Fatalf("正文仍含 MIME 源码: %q", got)
	}
}

// TestReadBodyMultipartQuotedPrintableHTML 验证无 text/plain 时回退到
// quoted-printable 的 HTML 部件并转为纯文本。
func TestReadBodyMultipartQuotedPrintableHTML(t *testing.T) {
	raw := "MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/alternative; boundary="B2"` + "\r\n\r\n" +
		"--B2\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"<html><body><p>=E9=AA=8C=E8=AF=81=E7=A0=81=EF=BC=9A654321</p></body></html>\r\n" +
		"--B2--\r\n"

	got, err := readBody(buildMessage(t, raw))
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if !strings.Contains(got, "654321") {
		t.Fatalf("未提取到验证码, got=%q", got)
	}
	if strings.Contains(got, "<p>") {
		t.Fatalf("HTML 标签未被清洗: %q", got)
	}
}

// TestReadBodyNestedMultipart 验证 multipart/mixed 内嵌 multipart/alternative
// 的常见结构(正文 + 附件)能正确取到正文。
func TestReadBodyNestedMultipart(t *testing.T) {
	raw := "MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="OUTER"` + "\r\n\r\n" +
		"--OUTER\r\n" +
		`Content-Type: multipart/alternative; boundary="INNER"` + "\r\n\r\n" +
		"--INNER\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"订单号 A-9527\r\n" +
		"--INNER--\r\n" +
		"--OUTER\r\n" +
		"Content-Type: application/pdf; name=\"a.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"JVBERi0xLjQK\r\n" +
		"--OUTER--\r\n"

	got, err := readBody(buildMessage(t, raw))
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if !strings.Contains(got, "A-9527") {
		t.Fatalf("未取到正文, got=%q", got)
	}
	if strings.Contains(got, "JVBERi0") {
		t.Fatalf("附件内容混入正文: %q", got)
	}
}

// TestReadBodyTruncatedMultipart 验证正文被截断(预览只取前 4KB)时
// 仍能降级返回已解析到的内容,而不是整封失败。
func TestReadBodyTruncatedMultipart(t *testing.T) {
	plain := strings.Repeat("正文内容 ", 200)
	raw := "MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/alternative; boundary="T"` + "\r\n\r\n" +
		"--T\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		plain + "\r\n" +
		"--T\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>HTML</p>" // 故意不给出结束 boundary,模拟截断

	got, err := readBody(buildMessage(t, raw))
	if err != nil {
		t.Fatalf("readBody 不应因截断失败: %v", err)
	}
	if !strings.Contains(got, "正文内容") {
		t.Fatalf("截断场景未返回已解析内容: %q", got)
	}
}

// TestDecodeTransferEncoding 覆盖传输编码解码的常见与异常输入。
func TestDecodeTransferEncoding(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		cte  string
		want string
	}{
		{"base64 带换行", "5L2g5aW9\r\n", "base64", "你好"},
		{"base64 无 padding", "5L2g5aW9", "base64 ", "你好"},
		{"base64 非法字符保持原样", "5L2g5", "base64", "5L2g5"},
		{"quoted-printable", "a=3Db", "quoted-printable", "a=b"},
		{"无编码", "plain", "7bit", "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(decodeTransferEncoding([]byte(tc.raw), tc.cte)); got != tc.want {
				t.Fatalf("decodeTransferEncoding(%q, %q) = %q, want %q", tc.raw, tc.cte, got, tc.want)
			}
		})
	}
}
