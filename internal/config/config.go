// Package config 负责加载、校验 YAML 配置，并解析时长、字节阈值与 ${ENV} 占位符。
package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是整个应用的配置根。所有时间间隔均为 time.Duration。
type Config struct {
	SourceDir       string               `yaml:"source_dir"`
	TargetFlow      TargetFlow           `yaml:"target_flow"`
	Archive         Archive              `yaml:"archive"`
	Telegram        Telegram             `yaml:"telegram"`
	SMTP            SMTP                 `yaml:"smtp"`
	Beneficiaries   []Beneficiary        `yaml:"beneficiaries"`
	Download        Download             `yaml:"download"`
	StateProtection StateProtection      `yaml:"state_protection"`
	Reliability     Reliability          `yaml:"reliability"`
	Logging         Logging              `yaml:"logging"`
	Templates       map[string]Templates `yaml:"templates"` // key 为语言码 zh/en

	// StateDir 是 state.db、加密包、日志的存放目录。默认取 Logging.File 的目录或 /data/state。
	StateDir string `yaml:"state_dir"`
}

// TargetFlow 是目标流程的节奏配置：每月确认、连续提醒、密码阶段打包、下载链接投递。
type TargetFlow struct {
	CheckinDayOfMonth      int      `yaml:"checkin_day_of_month"`
	ReminderCount          int      `yaml:"reminder_count"`    // 漏确认后连续提醒几次
	ReminderInterval       Duration `yaml:"reminder_interval"` // 两次提醒间隔（Go duration: 1m/2h/168h）
	PasswordDelayAfterWarn Duration `yaml:"password_delay_after_warn"`
	FileDelayAfterPassword Duration `yaml:"file_delay_after_password"`
	Timezone               string   `yaml:"timezone"`
}

type Archive struct {
	KeepArchives       int   `yaml:"keep_archives"`
	PasswordLength     int   `yaml:"password_length"`
	LargeFileThreshold Bytes `yaml:"large_file_threshold"`
}

type Telegram struct {
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
}

type SMTP struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	UseSSL    bool   `yaml:"use_ssl"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	FromName  string `yaml:"from_name"`
	FromEmail string `yaml:"from_email"`
}

type Beneficiary struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
	Lang  string `yaml:"lang"`
}

type Download struct {
	Mode         string           `yaml:"mode"` // self_hosted | s3
	LinkExpiry   Duration         `yaml:"link_expiry"`
	MaxDownloads int              `yaml:"max_downloads"`
	SelfHosted   SelfHostedConfig `yaml:"self_hosted"`
	S3           S3Config         `yaml:"s3"`
}

type SelfHostedConfig struct {
	PublicBaseURL string `yaml:"public_base_url"`
	ListenPort    int    `yaml:"listen_port"`
}

type S3Config struct {
	Endpoint      string   `yaml:"endpoint"`
	Bucket        string   `yaml:"bucket"`
	Region        string   `yaml:"region"`
	AccessKey     string   `yaml:"access_key"`
	SecretKey     string   `yaml:"secret_key"`
	PresignExpiry Duration `yaml:"presign_expiry"`
}

type StateProtection struct {
	EncryptPasswordField bool   `yaml:"encrypt_password_field"`
	MasterPassphrase     string `yaml:"master_passphrase"`
}

type Reliability struct {
	HeartbeatEnabled  bool              `yaml:"heartbeat_enabled"`
	HeartbeatInterval Duration          `yaml:"heartbeat_interval"`
	Healthcheck       HealthcheckConfig `yaml:"healthcheck"`
}

type HealthcheckConfig struct {
	Enabled  bool     `yaml:"enabled"`
	PingURL  string   `yaml:"ping_url"`
	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`
}

type Logging struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// Templates 是单一语言的全部文案模板。占位符用 {name}、{password}、{url} 等。
type Templates struct {
	CheckinTelegram       string `yaml:"checkin_telegram"`
	CheckinButtonText     string `yaml:"checkin_button_text"`
	CheckinAcceptedReply  string `yaml:"checkin_accepted_reply"`
	CheckinExpiredReply   string `yaml:"checkin_expired_reply"`
	CheckinErrorReply     string `yaml:"checkin_error_reply"`
	DailyReminderTelegram string `yaml:"daily_reminder_telegram"`
	FinalReminderTelegram string `yaml:"final_reminder_telegram"`
	WarnStageTelegram     string `yaml:"warn_stage_telegram"`
	PasswordStageTelegram string `yaml:"password_stage_telegram"`
	FileStageTelegram     string `yaml:"file_stage_telegram"`
	CancelFlowTelegram    string `yaml:"cancel_flow_telegram"`
	HeartbeatTelegram     string `yaml:"heartbeat_telegram"`
	FinalWarningTelegram  string `yaml:"final_warning_telegram"`
	WarnEmailSubject      string `yaml:"warn_email_subject"`
	WarnEmailBody         string `yaml:"warn_email_body"`
	PasswordEmailSubject  string `yaml:"password_email_subject"`
	PasswordEmailBody     string `yaml:"password_email_body"`
	FileEmailSubject      string `yaml:"file_email_subject"`
	FileEmailBodyLink     string `yaml:"file_email_body_link"`
}

// Duration 包装 time.Duration，让 YAML 里可写 "24h"、"72h" 这样的字符串。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("无法解析时长 %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Bytes 让 YAML 里可写 "20MB"、"500KB"、"1GB" 这样的字节阈值。
type Bytes int64

var bytesRe = regexp.MustCompile(`^(?i)\s*(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)?\s*$`)

func (b *Bytes) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		// 也允许直接写裸数字
		var n int64
		if err2 := value.Decode(&n); err2 == nil {
			*b = Bytes(n)
			return nil
		}
		return err
	}
	m := bytesRe.FindStringSubmatch(s)
	if m == nil {
		return fmt.Errorf("无法解析字节大小 %q（示例：20MB、500KB）", s)
	}
	num, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return err
	}
	mult := map[string]float64{"": 1, "B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40}
	*b = Bytes(int64(num * mult[strings.ToUpper(m[2])]))
	return nil
}

func (b Bytes) Int64() int64 { return int64(b) }

// envRe 匹配 ${VAR} 占位符。
var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv 把内容里的 ${VAR} 替换为对应环境变量；未设置的变量替换为空串。
func expandEnv(raw []byte) []byte {
	return envRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		name := envRe.FindSubmatch(m)[1]
		return []byte(os.Getenv(string(name)))
	})
}

// Load 读取并解析配置文件，展开 ${ENV} 占位符，应用默认值，最后校验。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s 失败: %w", path, err)
	}
	raw = expandEnv(raw)

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Archive.KeepArchives == 0 {
		c.Archive.KeepArchives = 3
	}
	if c.Archive.PasswordLength == 0 {
		c.Archive.PasswordLength = 32
	}
	if c.Archive.LargeFileThreshold == 0 {
		c.Archive.LargeFileThreshold = Bytes(20 << 20) // 20MB
	}
	if c.TargetFlow.CheckinDayOfMonth == 0 {
		c.TargetFlow.CheckinDayOfMonth = 1
	}
	if c.TargetFlow.ReminderCount == 0 {
		c.TargetFlow.ReminderCount = 7
	}
	if c.TargetFlow.ReminderInterval.Std() == 0 {
		c.TargetFlow.ReminderInterval = Duration(24 * time.Hour)
	}
	if c.TargetFlow.PasswordDelayAfterWarn.Std() == 0 {
		c.TargetFlow.PasswordDelayAfterWarn = Duration(72 * time.Hour)
	}
	if c.TargetFlow.FileDelayAfterPassword.Std() == 0 {
		c.TargetFlow.FileDelayAfterPassword = Duration(168 * time.Hour)
	}
	if c.TargetFlow.Timezone == "" {
		c.TargetFlow.Timezone = "Asia/Shanghai"
	}
	if c.Download.Mode == "" {
		c.Download.Mode = "self_hosted"
	}
	if c.Download.LinkExpiry.Std() == 0 {
		c.Download.LinkExpiry = Duration(336 * time.Hour)
	}
	if c.Download.MaxDownloads == 0 {
		c.Download.MaxDownloads = 5
	}
	if c.Download.SelfHosted.ListenPort == 0 {
		c.Download.SelfHosted.ListenPort = 8080
	}
	if c.Reliability.Healthcheck.Interval.Std() == 0 {
		c.Reliability.Healthcheck.Interval = Duration(10 * time.Minute)
	}
	if c.Reliability.Healthcheck.Timeout.Std() == 0 {
		c.Reliability.Healthcheck.Timeout = Duration(10 * time.Second)
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "INFO"
	}
	if c.StateDir == "" {
		c.StateDir = "/data/state"
	}
}

// Validate 检查关键字段，缺失或不合理时报错。尽早暴露配置问题。
func (c *Config) Validate() error {
	var errs []string
	if c.SourceDir == "" {
		errs = append(errs, "source_dir 不能为空")
	}
	if c.TargetFlow.CheckinDayOfMonth < 1 || c.TargetFlow.CheckinDayOfMonth > 31 {
		errs = append(errs, "target_flow.checkin_day_of_month 必须在 1..31 之间")
	}
	if c.TargetFlow.ReminderCount < 1 {
		errs = append(errs, "target_flow.reminder_count 必须 >= 1")
	}
	if c.TargetFlow.ReminderInterval.Std() <= 0 {
		errs = append(errs, "target_flow.reminder_interval 必须为正")
	}
	if c.TargetFlow.PasswordDelayAfterWarn.Std() <= 0 {
		errs = append(errs, "target_flow.password_delay_after_warn 必须为正")
	}
	if c.TargetFlow.FileDelayAfterPassword.Std() <= 0 {
		errs = append(errs, "target_flow.file_delay_after_password 必须为正")
	}
	if _, err := time.LoadLocation(c.TargetFlow.Timezone); err != nil {
		errs = append(errs, fmt.Sprintf("target_flow.timezone 无法加载: %v", err))
	}
	if c.Telegram.BotToken == "" {
		errs = append(errs, "telegram.bot_token 不能为空（检查 ${TELEGRAM_BOT_TOKEN} 是否已设置）")
	}
	if c.Telegram.ChatID == 0 {
		errs = append(errs, "telegram.chat_id 不能为 0")
	}
	if c.SMTP.Host == "" {
		errs = append(errs, "smtp.host 不能为空")
	}
	if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
		errs = append(errs, "smtp.port 必须在 1..65535 之间")
	}
	if strings.TrimSpace(c.SMTP.Username) == "" {
		errs = append(errs, "smtp.username 不能为空")
	}
	if strings.TrimSpace(c.SMTP.Password) == "" {
		errs = append(errs, "smtp.password 不能为空（检查 ${SMTP_PASSWORD} 是否已设置）")
	}
	if c.SMTP.FromEmail == "" {
		errs = append(errs, "smtp.from_email 不能为空")
	} else if !isEmail(c.SMTP.FromEmail) {
		errs = append(errs, "smtp.from_email 格式不正确")
	}
	if len(c.Beneficiaries) == 0 {
		errs = append(errs, "beneficiaries 至少需要一个受益人")
	}
	for i, b := range c.Beneficiaries {
		if strings.TrimSpace(b.Name) == "" {
			errs = append(errs, fmt.Sprintf("beneficiaries[%d].name 不能为空", i))
		}
		if b.Email == "" {
			errs = append(errs, fmt.Sprintf("beneficiaries[%d].email 不能为空", i))
		} else if !isEmail(b.Email) {
			errs = append(errs, fmt.Sprintf("beneficiaries[%d].email 格式不正确", i))
		}
		if b.Lang == "" {
			c.Beneficiaries[i].Lang = "zh" // 默认中文
		} else if _, ok := c.Templates[b.Lang]; !ok {
			errs = append(errs, fmt.Sprintf("beneficiaries[%d].lang=%q 未在 templates 中配置", i, b.Lang))
		}
	}
	if c.Download.Mode != "self_hosted" && c.Download.Mode != "s3" {
		errs = append(errs, "download.mode 必须为 self_hosted 或 s3")
	}
	if c.Download.LinkExpiry.Std() <= 0 {
		errs = append(errs, "download.link_expiry 必须为正")
	}
	if c.Download.MaxDownloads < 1 {
		errs = append(errs, "download.max_downloads 必须 >= 1")
	}
	if c.Download.Mode == "self_hosted" {
		if c.Download.SelfHosted.PublicBaseURL == "" {
			errs = append(errs, "download.self_hosted.public_base_url 不能为空")
		} else if !isHTTPURL(c.Download.SelfHosted.PublicBaseURL) {
			errs = append(errs, "download.self_hosted.public_base_url 必须是 http 或 https URL")
		}
		if c.Download.SelfHosted.ListenPort < 1 || c.Download.SelfHosted.ListenPort > 65535 {
			errs = append(errs, "download.self_hosted.listen_port 必须在 1..65535 之间")
		}
	}
	if c.Download.Mode == "s3" {
		if !isHTTPURL(c.Download.S3.Endpoint) {
			errs = append(errs, "download.s3.endpoint 必须是 http 或 https URL")
		}
		if strings.TrimSpace(c.Download.S3.Bucket) == "" {
			errs = append(errs, "download.s3.bucket 不能为空")
		}
		if strings.TrimSpace(c.Download.S3.Region) == "" {
			errs = append(errs, "download.s3.region 不能为空")
		}
		if strings.TrimSpace(c.Download.S3.AccessKey) == "" {
			errs = append(errs, "download.s3.access_key 不能为空")
		}
		if strings.TrimSpace(c.Download.S3.SecretKey) == "" {
			errs = append(errs, "download.s3.secret_key 不能为空")
		}
		if c.Download.S3.PresignExpiry.Std() <= 0 {
			errs = append(errs, "download.s3.presign_expiry 必须为正")
		}
	}
	if c.StateProtection.EncryptPasswordField && c.StateProtection.MasterPassphrase == "" {
		errs = append(errs, "启用 state_protection.encrypt_password_field 时 master_passphrase 不能为空")
	}
	if c.Reliability.Healthcheck.Enabled {
		if !isHTTPURL(c.Reliability.Healthcheck.PingURL) {
			errs = append(errs, "reliability.healthcheck.ping_url 必须是 http 或 https URL")
		}
		if c.Reliability.Healthcheck.Interval.Std() <= 0 {
			errs = append(errs, "reliability.healthcheck.interval 必须为正")
		}
		if c.Reliability.Healthcheck.Timeout.Std() <= 0 {
			errs = append(errs, "reliability.healthcheck.timeout 必须为正")
		}
		if c.Reliability.Healthcheck.Timeout.Std() >= c.Reliability.Healthcheck.Interval.Std() {
			errs = append(errs, "reliability.healthcheck.timeout 必须小于 interval")
		}
	}
	if t, ok := c.Templates["zh"]; !ok {
		errs = append(errs, "templates.zh 不能为空")
	} else {
		required := map[string]string{
			"checkin_telegram":        t.CheckinTelegram,
			"checkin_button_text":     t.CheckinButtonText,
			"checkin_accepted_reply":  t.CheckinAcceptedReply,
			"checkin_expired_reply":   t.CheckinExpiredReply,
			"checkin_error_reply":     t.CheckinErrorReply,
			"daily_reminder_telegram": t.DailyReminderTelegram,
			"final_reminder_telegram": t.FinalReminderTelegram,
			"warn_stage_telegram":     t.WarnStageTelegram,
			"password_stage_telegram": t.PasswordStageTelegram,
			"file_stage_telegram":     t.FileStageTelegram,
			"cancel_flow_telegram":    t.CancelFlowTelegram,
			"warn_email_subject":      t.WarnEmailSubject,
			"warn_email_body":         t.WarnEmailBody,
			"password_email_subject":  t.PasswordEmailSubject,
			"password_email_body":     t.PasswordEmailBody,
			"file_email_subject":      t.FileEmailSubject,
			"file_email_body_link":    t.FileEmailBodyLink,
		}
		for name, value := range required {
			if strings.TrimSpace(value) == "" {
				errs = append(errs, fmt.Sprintf("templates.zh.%s 不能为空", name))
			}
		}
		if c.Reliability.HeartbeatEnabled && strings.TrimSpace(t.HeartbeatTelegram) == "" {
			errs = append(errs, "templates.zh.heartbeat_telegram 在 heartbeat_enabled=true 时不能为空")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ValidateRuntimePaths 检查依赖本机文件系统的路径。它和 Validate 分离，
// 方便单元测试纯配置，也避免加载配置时创建目录。
func (c *Config) ValidateRuntimePaths() error {
	var errs []string
	if err := requireReadableDir(c.SourceDir); err != nil {
		errs = append(errs, fmt.Sprintf("source_dir 不可读: %v", err))
	}
	if err := requireWritableDir(c.StateDir); err != nil {
		errs = append(errs, fmt.Sprintf("state_dir 不可写: %v", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("运行时路径校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func requireReadableDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("不是目录")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	_ = entries
	return nil
}

func requireWritableDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("不是目录")
	}
	probe, err := os.CreateTemp(path, ".ifgone-write-test-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func isEmail(value string) bool {
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// Lang 返回指定语言的模板，找不到时回退到 zh，再回退到任意一个。
func (c *Config) Lang(lang string) Templates {
	if t, ok := c.Templates[lang]; ok {
		return t
	}
	if t, ok := c.Templates["zh"]; ok {
		return t
	}
	for _, t := range c.Templates {
		return t
	}
	return Templates{}
}
