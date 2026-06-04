package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func textNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: "!!str"}
}

func minimalConfig() string {
	return `
source_dir: /tmp/source
state_dir: /tmp/state
telegram:
  bot_token: ${TEST_TELEGRAM_TOKEN}
  chat_id: 123
smtp:
  host: smtp.example.com
  port: 465
  use_ssl: true
  username: me@example.com
  password: ${TEST_SMTP_PASSWORD}
  from_name: owner
  from_email: me@example.com
beneficiaries:
  - {name: "张三", email: zhangsan@example.com, lang: zh}
download:
  mode: self_hosted
  self_hosted:
    public_base_url: https://example.com
templates:
  zh:
    checkin_telegram: "确认"
    checkin_button_text: "✅ 一切正常"
    checkin_accepted_reply: "已确认"
    checkin_expired_reply: "已过期"
    checkin_error_reply: "确认出错"
    daily_reminder_telegram: "提醒<N>"
    final_reminder_telegram: "最后提醒"
    warn_stage_telegram: "预提醒阶段"
    password_stage_telegram: "密码阶段"
    file_stage_telegram: "文件阶段"
    cancel_flow_telegram: "已取消"
    heartbeat_telegram: "心跳"
    warn_email_subject: "预提醒"
    warn_email_body: "预提醒正文"
    password_email_subject: "密码"
    password_email_body: "密码正文"
    file_email_subject: "下载"
    file_email_body_link: "下载正文"
`
}

func TestLoadExpandsEnvAndAppliesDefaults(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")

	cfg, err := Load(writeConfig(t, minimalConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.BotToken != "bot-token" || cfg.SMTP.Password != "smtp-pass" {
		t.Fatalf("env 未展开: telegram=%q smtp=%q", cfg.Telegram.BotToken, cfg.SMTP.Password)
	}
	if cfg.TargetFlow.CheckinDayOfMonth != 1 {
		t.Fatalf("默认确认日不对: %d", cfg.TargetFlow.CheckinDayOfMonth)
	}
	if cfg.TargetFlow.ReminderCount != 7 {
		t.Fatalf("默认连续提醒次数不对: %d", cfg.TargetFlow.ReminderCount)
	}
	if cfg.TargetFlow.ReminderInterval.Std() != 24*time.Hour {
		t.Fatalf("提醒间隔默认值不对: %s", cfg.TargetFlow.ReminderInterval.Std())
	}
	if !cfg.TargetFlow.ReminderInterval.IsDayBased() {
		t.Fatal("默认提醒间隔应保留按天语义")
	}
	if cfg.TargetFlow.PasswordDelayAfterWarn.Std() != 72*time.Hour {
		t.Fatalf("密码延迟默认值不对: %s", cfg.TargetFlow.PasswordDelayAfterWarn.Std())
	}
	if !cfg.TargetFlow.PasswordDelayAfterWarn.IsDayBased() {
		t.Fatal("默认密码延迟应保留按天语义")
	}
	if cfg.TargetFlow.FileDelayAfterPassword.Std() != 168*time.Hour {
		t.Fatalf("文件延迟默认值不对: %s", cfg.TargetFlow.FileDelayAfterPassword.Std())
	}
	if !cfg.TargetFlow.FileDelayAfterPassword.IsDayBased() {
		t.Fatal("默认文件延迟应保留按天语义")
	}
	if cfg.TargetFlow.SendTimeOfDay != "00:00" {
		t.Fatalf("默认发送时间不对: %s", cfg.TargetFlow.SendTimeOfDay)
	}
	if cfg.TargetFlow.Timezone != "Asia/Shanghai" {
		t.Fatalf("默认时区不对: %s", cfg.TargetFlow.Timezone)
	}
	if cfg.Download.LinkExpiry.Std() != 336*time.Hour || cfg.Download.MaxDownloads != 5 || cfg.Download.SelfHosted.ListenPort != 8080 {
		t.Fatalf("下载默认值不对: expiry=%s max=%d port=%d", cfg.Download.LinkExpiry.Std(), cfg.Download.MaxDownloads, cfg.Download.SelfHosted.ListenPort)
	}
	if !cfg.Download.LinkExpiry.IsDayBased() {
		t.Fatal("默认下载有效期应保留按天语义")
	}
	if cfg.Reliability.Healthcheck.Interval.Std() != 10*time.Minute || cfg.Reliability.Healthcheck.Timeout.Std() != 10*time.Second {
		t.Fatalf("探活默认值不对: interval=%s timeout=%s", cfg.Reliability.Healthcheck.Interval.Std(), cfg.Reliability.Healthcheck.Timeout.Std())
	}
}

func TestHeartbeatTemplateRequiredOnlyWhenHeartbeatEnabled(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")

	raw := strings.Replace(minimalConfig(), `    heartbeat_telegram: "心跳"`+"\n", "", 1)
	if _, err := Load(writeConfig(t, raw)); err != nil {
		t.Fatalf("heartbeat 关闭时不应要求 heartbeat_telegram: %v", err)
	}

	raw = strings.Replace(raw, "download:\n", "reliability:\n  heartbeat_enabled: true\n\ndownload:\n", 1)
	_, err := Load(writeConfig(t, raw))
	if err == nil || !strings.Contains(err.Error(), "templates.zh.heartbeat_telegram") {
		t.Fatalf("heartbeat 开启时应要求 heartbeat_telegram，实际: %v", err)
	}
}

func TestValidateRejectsInvalidTargetFlowAndDownload(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := strings.Replace(minimalConfig(), "    public_base_url: https://example.com", "    public_base_url: \"\"", 1)
	raw = strings.Replace(raw, "download:\n  mode: self_hosted", "download:\n  mode: self_hosted\n  link_expiry: -1s\n  max_downloads: -1", 1)
	raw += `
target_flow:
  checkin_day_of_month: 32
  reminder_count: -1
  reminder_interval: -1s
  password_delay_after_warn: -1s
  file_delay_after_password: -1s
  send_time_of_day: 24:00
  timezone: Not/AZone
`
	raw = strings.Replace(raw, "    public_base_url: \"\"", "    public_base_url: \"\"\n    listen_port: 70000", 1)
	_, err := Load(writeConfig(t, raw))
	if err == nil {
		t.Fatal("期望配置校验失败")
	}
	msg := err.Error()
	for _, want := range []string{
		"target_flow.checkin_day_of_month",
		"target_flow.reminder_count",
		"target_flow.password_delay_after_warn",
		"target_flow.file_delay_after_password",
		"target_flow.send_time_of_day",
		"target_flow.timezone",
		"download.link_expiry",
		"download.max_downloads",
		"download.self_hosted.public_base_url",
		"download.self_hosted.listen_port",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
}

func TestValidateRequiresMasterPassphraseWhenEncryptingStatePassword(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := minimalConfig() + `
state_protection:
  encrypt_password_field: true
  master_passphrase: ""
`
	_, err := Load(writeConfig(t, raw))
	if err == nil || !strings.Contains(err.Error(), "master_passphrase") {
		t.Fatalf("期望 master_passphrase 校验失败，实际 %v", err)
	}
}

func TestValidateRejectsInvalidHealthcheckConfig(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := minimalConfig() + `
reliability:
  healthcheck:
    enabled: true
    ping_url: ftp://example.com/ping
    interval: 5s
    timeout: 5s
`
	_, err := Load(writeConfig(t, raw))
	if err == nil {
		t.Fatal("期望 healthcheck 配置校验失败")
	}
	msg := err.Error()
	for _, want := range []string{
		"reliability.healthcheck.ping_url",
		"reliability.healthcheck.timeout",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
}

func TestValidateRequiresCriticalZhTemplates(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := strings.Replace(minimalConfig(), `    warn_email_body: "预提醒正文"`, `    warn_email_body: ""`, 1)
	_, err := Load(writeConfig(t, raw))
	if err == nil || !strings.Contains(err.Error(), "templates.zh.warn_email_body") {
		t.Fatalf("期望模板校验失败，实际 %v", err)
	}
}

func TestValidateRejectsInvalidSMTPBeneficiariesAndDownloadURLs(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := strings.Replace(minimalConfig(), "  port: 465", "  port: 70000", 1)
	raw = strings.Replace(raw, "  from_email: me@example.com", "  from_email: not-an-email", 1)
	raw = strings.Replace(raw, `  - {name: "张三", email: zhangsan@example.com, lang: zh}`, `  - {name: "", email: bad-email, lang: en}`, 1)
	raw = strings.Replace(raw, "    public_base_url: https://example.com", "    public_base_url: ftp://example.com", 1)

	_, err := Load(writeConfig(t, raw))
	if err == nil {
		t.Fatal("期望配置校验失败")
	}
	msg := err.Error()
	for _, want := range []string{
		"smtp.port",
		"smtp.from_email",
		"beneficiaries[0].name",
		"beneficiaries[0].email",
		"beneficiaries[0].lang",
		"download.self_hosted.public_base_url",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
}

func TestValidateRejectsIncompleteS3Config(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "bot-token")
	t.Setenv("TEST_SMTP_PASSWORD", "smtp-pass")
	raw := strings.Replace(minimalConfig(), "download:\n  mode: self_hosted\n  self_hosted:\n    public_base_url: https://example.com", `download:
  mode: s3
  s3:
    endpoint: ""
    bucket: ""
    region: ""
    access_key: ""
    secret_key: ""
    presign_expiry: -1s`, 1)

	_, err := Load(writeConfig(t, raw))
	if err == nil {
		t.Fatal("期望 S3 配置校验失败")
	}
	msg := err.Error()
	for _, want := range []string{
		"download.s3.endpoint",
		"download.s3.bucket",
		"download.s3.region",
		"download.s3.access_key",
		"download.s3.secret_key",
		"download.s3.presign_expiry",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
}

func TestValidateRuntimePaths(t *testing.T) {
	source := t.TempDir()
	state := t.TempDir()
	cfg := &Config{SourceDir: source, StateDir: state}
	if err := cfg.ValidateRuntimePaths(); err != nil {
		t.Fatalf("有效路径不应失败: %v", err)
	}

	cfg.SourceDir = filepath.Join(t.TempDir(), "missing")
	err := cfg.ValidateRuntimePaths()
	if err == nil || !strings.Contains(err.Error(), "source_dir") {
		t.Fatalf("期望 source_dir 路径失败，实际 %v", err)
	}

	filePath := filepath.Join(t.TempDir(), "state-file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.SourceDir = source
	cfg.StateDir = filePath
	err = cfg.ValidateRuntimePaths()
	if err == nil || !strings.Contains(err.Error(), "state_dir") {
		t.Fatalf("期望 state_dir 文件路径失败，实际 %v", err)
	}
}

func TestDurationBytesAndLangFallback(t *testing.T) {
	var d Duration
	if err := d.UnmarshalYAML(textNode("90m")); err != nil {
		t.Fatal(err)
	}
	if d.Std() != 90*time.Minute {
		t.Fatalf("duration=%s", d.Std())
	}
	if d.IsDayBased() {
		t.Fatal("90m 不应被视为按天语义")
	}
	var day Duration
	if err := day.UnmarshalYAML(textNode("3d")); err != nil {
		t.Fatal(err)
	}
	if day.Std() != 72*time.Hour {
		t.Fatalf("3d 标准时长不对: %s", day.Std())
	}
	days, ok := day.DayCount()
	if !ok || days != 3 || !day.IsDayBased() {
		t.Fatalf("3d 语义信息不对: ok=%v days=%d dayBased=%v", ok, days, day.IsDayBased())
	}
	var b Bytes
	if err := b.UnmarshalYAML(textNode("1.5MB")); err != nil {
		t.Fatal(err)
	}
	if b.Int64() != int64(1.5*1024*1024) {
		t.Fatalf("bytes=%d", b.Int64())
	}
	cfg := &Config{Templates: map[string]Templates{"zh": {CheckinTelegram: "中文"}}}
	if cfg.Lang("en").CheckinTelegram != "中文" {
		t.Fatal("语言缺失时应回退 zh")
	}
}

func TestDurationRejectsFractionalDays(t *testing.T) {
	var d Duration
	err := d.UnmarshalYAML(textNode("1.5d"))
	if err == nil || !strings.Contains(err.Error(), "无法解析时长") {
		t.Fatalf("期望 1.5d 被拒绝，实际 %v", err)
	}
}
