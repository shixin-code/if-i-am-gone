// Package mailer 通过 SMTP 给受益人发送三阶段邮件（预警/密码/文件）。
// MVP 阶段文件投递走附件；超阈值的大文件链接投递由 delivery 包决定。
package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer 持有 SMTP 连接参数。
type Mailer struct {
	Host      string
	Port      int
	UseSSL    bool
	Username  string
	Password  string
	FromName  string
	FromEmail string
}

// Attachment 是一个邮件附件。
type Attachment struct {
	Filename string
	Content  []byte
}

// Message 是一封待发邮件。
type Message struct {
	To          string
	ToName      string
	Subject     string
	Body        string // 纯文本正文
	Attachments []Attachment
}

// Send 发送一封邮件。SSL(465) 走隐式 TLS；其余端口走 STARTTLS。
func (m *Mailer) Send(msg Message) error {
	addr := net.JoinHostPort(m.Host, fmt.Sprintf("%d", m.Port))
	auth := smtp.PlainAuth("", m.Username, m.Password, m.Host)
	raw := m.build(msg)

	if m.UseSSL {
		return m.sendSSL(addr, auth, msg.To, raw)
	}
	return smtp.SendMail(addr, auth, m.FromEmail, []string{msg.To}, raw)
}

// sendSSL 处理隐式 TLS（端口 465）。
func (m *Mailer) sendSSL(addr string, auth smtp.Auth, to string, raw []byte) error {
	tlsConf := &tls.Config{ServerName: m.Host}
	conn, err := tls.Dial("tcp", addr, tlsConf)
	if err != nil {
		return fmt.Errorf("TLS 连接 SMTP 失败: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return fmt.Errorf("创建 SMTP 客户端失败: %w", err)
	}
	defer c.Quit()

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	if err := c.Mail(m.FromEmail); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	return w.Close()
}

// build 构造符合 RFC 5322/MIME 的邮件字节。无附件时用纯文本，
// 有附件时用 multipart/mixed。附件用 base64 编码。
func (m *Mailer) build(msg Message) []byte {
	var b strings.Builder
	from := fmt.Sprintf("%s <%s>", encodeHeader(m.FromName), m.FromEmail)
	to := msg.To
	if msg.ToName != "" {
		to = fmt.Sprintf("%s <%s>", encodeHeader(msg.ToName), msg.To)
	}
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + encodeHeader(msg.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) == 0 {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.Body)
		return []byte(b.String())
	}

	boundary := "----=_ifgone_boundary_7c2f1a9e"
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")

	// 正文部分
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(msg.Body + "\r\n")

	// 附件部分
	for _, att := range msg.Attachments {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: application/octet-stream\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", att.Filename))
		b.WriteString(base64Wrap(att.Content))
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// encodeHeader 对含非 ASCII 的头部做 RFC 2047 MIME 编码（如中文姓名/主题）。
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return mimeEncode(s)
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
