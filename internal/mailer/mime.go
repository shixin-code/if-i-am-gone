package mailer

import (
	"encoding/base64"
	"strings"
)

// mimeEncode 对字符串做 RFC 2047 "B" 编码（UTF-8 + base64），用于邮件头部。
func mimeEncode(s string) string {
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

// base64Wrap 对内容做 base64 编码，并按 76 字符折行（RFC 2045）。
func base64Wrap(content []byte) string {
	encoded := base64.StdEncoding.EncodeToString(content)
	var b strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
