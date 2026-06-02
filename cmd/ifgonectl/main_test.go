package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

func writeCtlConfig(t *testing.T, dir string) string {
	t.Helper()
	source := filepath.Join(dir, "source")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	body := `
source_dir: ` + source + `
state_dir: ` + stateDir + `
target_flow:
  checkin_day_of_month: 1
  reminder_count: 7
  reminder_interval: 24h
  password_delay_after_warn: 72h
  file_delay_after_password: 168h
  timezone: UTC
archive:
  keep_archives: 3
  password_length: 16
  large_file_threshold: 20MB
telegram:
  bot_token: token
  chat_id: 123
smtp:
  host: smtp.example.com
  port: 465
  use_ssl: true
  username: me@example.com
  password: pass
  from_name: owner
  from_email: me@example.com
beneficiaries:
  - {name: "张三", email: zhangsan@example.com, lang: zh}
download:
  mode: self_hosted
  link_expiry: 24h
  max_downloads: 3
  self_hosted:
    public_base_url: https://example.com
    listen_port: 8080
state_protection:
  encrypt_password_field: false
  master_passphrase: passphrase
reliability:
  heartbeat_enabled: false
  heartbeat_interval: 168h
logging:
  level: INFO
  file: ` + filepath.Join(stateDir, "app.log") + `
templates:
  zh:
    checkin_telegram: "确认"
    checkin_button_text: "确认正常"
    checkin_accepted_reply: "已确认"
    checkin_expired_reply: "已过期"
    checkin_error_reply: "出错"
    daily_reminder_telegram: "提醒<N>"
    final_reminder_telegram: "最后提醒"
    warn_stage_telegram: "预提醒阶段"
    password_stage_telegram: "密码阶段"
    file_stage_telegram: "文件阶段"
    cancel_flow_telegram: "取消"
    heartbeat_telegram: "心跳"
    warn_email_subject: "预提醒"
    warn_email_body: "预提醒正文"
    password_email_subject: "密码"
    password_email_body: "密码正文"
    file_email_subject: "下载"
    file_email_body_link: "下载正文"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatusAndDryRun(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeCtlConfig(t, dir)
	var out bytes.Buffer
	if err := run([]string{"status", "--config", cfgPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "phase: ALIVE") {
		t.Fatalf("status 输出不对: %s", out.String())
	}

	out.Reset()
	if err := run([]string{"dry-run", "--config", cfgPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "next_action:") {
		t.Fatalf("dry-run 输出不对: %s", out.String())
	}
}

func TestPackAndSaveState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeCtlConfig(t, dir)
	var out bytes.Buffer
	if err := run([]string{"pack", "--config", cfgPath, "--save-state"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "archive_path:") || !strings.Contains(text, "state_saved: true") {
		t.Fatalf("pack 输出不对: %s", text)
	}

	store, err := state.Open(filepath.Join(dir, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.CurrentArchivePath == "" || st.CurrentArchiveSHA256 == "" || st.CurrentArchivePassword == "" || st.LastPackAt == nil {
		t.Fatalf("state 未保存打包结果: %+v", st)
	}
}

func TestCleanupTokens(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeCtlConfig(t, dir)
	store, err := state.Open(filepath.Join(dir, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDownloadToken(state.DownloadToken{
		Token:        "expired",
		ArchivePath:  "/tmp/a.zip",
		Beneficiary:  "a@example.com",
		ExpiresAt:    time.Now().Add(-time.Hour),
		MaxDownloads: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	var out bytes.Buffer
	if err := run([]string{"cleanup-tokens", "--config", cfgPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cleanup_expired_tokens: ok") {
		t.Fatalf("cleanup 输出不对: %s", out.String())
	}
}

func TestTestEmailBuildsAndSendsMessage(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeCtlConfig(t, dir)
	var sent mailer.Message
	oldSend := sendTestEmail
	sendTestEmail = func(cfg *config.Config, msg mailer.Message) error {
		sent = msg
		return nil
	}
	t.Cleanup(func() { sendTestEmail = oldSend })

	var out bytes.Buffer
	if err := run([]string{"test-email", "--config", cfgPath, "--to", "receiver@example.com"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "test_email_sent: receiver@example.com") {
		t.Fatalf("test-email 输出不对: %s", out.String())
	}
	if sent.To != "receiver@example.com" || !strings.Contains(sent.Subject, "SMTP 测试邮件") || !strings.Contains(sent.Body, "ifgonectl 测试邮件") {
		t.Fatalf("测试邮件内容不对: %+v", sent)
	}
}
