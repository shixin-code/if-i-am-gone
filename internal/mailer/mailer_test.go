package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testMailer() *Mailer {
	return &Mailer{
		FromName:  "意外开关",
		FromEmail: "owner@example.com",
	}
}

func TestBuildPlainTextMessageEncodesChineseHeaders(t *testing.T) {
	raw := string(testMailer().build(Message{
		To:      "a@example.com",
		ToName:  "张三",
		Subject: "重要通知",
		Body:    "这是一封测试邮件",
	}))

	for _, want := range []string{
		"From: =?UTF-8?B?5oSP5aSW5byA5YWz?= <owner@example.com>\r\n",
		"To: =?UTF-8?B?5byg5LiJ?= <a@example.com>\r\n",
		"Subject: =?UTF-8?B?6YeN6KaB6YCa55+l?=\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"Content-Transfer-Encoding: 8bit\r\n\r\n这是一封测试邮件",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("邮件缺少片段 %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "multipart/mixed") {
		t.Fatal("无附件邮件不应是 multipart")
	}
}

func TestBuildMultipartMessageWithAttachment(t *testing.T) {
	raw := string(testMailer().build(Message{
		To:      "a@example.com",
		Subject: "file",
		Body:    "正文",
		Attachments: []Attachment{{
			Filename: "archive.zip",
			Content:  []byte("zip-content"),
		}},
	}))

	for _, want := range []string{
		"Content-Type: multipart/mixed; boundary=\"----=_ifgone_boundary_7c2f1a9e\"",
		"------=_ifgone_boundary_7c2f1a9e\r\nContent-Type: text/plain; charset=UTF-8",
		"Content-Disposition: attachment; filename=\"archive.zip\"",
		base64.StdEncoding.EncodeToString([]byte("zip-content")),
		"------=_ifgone_boundary_7c2f1a9e--\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("multipart 邮件缺少片段 %q:\n%s", want, raw)
		}
	}
}

func TestBase64WrapUses76CharacterLines(t *testing.T) {
	wrapped := base64Wrap([]byte(strings.Repeat("x", 120)))
	lines := strings.Split(strings.TrimSuffix(wrapped, "\r\n"), "\r\n")
	if len(lines) < 2 {
		t.Fatalf("期望多行 base64，实际 %q", wrapped)
	}
	for i, line := range lines[:len(lines)-1] {
		if len(line) != 76 {
			t.Fatalf("第 %d 行长度=%d", i, len(line))
		}
	}
	if len(lines[len(lines)-1]) > 76 {
		t.Fatalf("最后一行过长: %d", len(lines[len(lines)-1]))
	}
}

func TestEncodeHeaderKeepsASCII(t *testing.T) {
	if got := encodeHeader("plain subject"); got != "plain subject" {
		t.Fatalf("ASCII header 不应编码: %q", got)
	}
}
