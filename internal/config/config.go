// Package config 负责加载、校验 YAML 配置，并解析时长、字节阈值与 ${ENV} 占位符。
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是整个应用的配置根。所有时间间隔均为 time.Duration。
type Config struct {
	SourceDir       string          `yaml:"source_dir"`
	Intervals       Intervals       `yaml:"intervals"`
	Archive         Archive         `yaml:"archive"`
	Telegram        Telegram        `yaml:"telegram"`
	SMTP            SMTP            `yaml:"smtp"`
	Beneficiaries   []Beneficiary   `yaml:"beneficiaries"`
	Download        Download        `yaml:"download"`
	StateProtection StateProtection `yaml:"state_protection"`
	Reliability     Reliability     `yaml:"reliability"`
	Logging         Logging         `yaml:"logging"`
	Templates       map[string]Templates `yaml:"templates"` // key 为语言码 zh/en

	// StateDir 是 state.db、加密包、日志的存放目录。默认取 Logging.File 的目录或 /data/state。
	StateDir string `yaml:"state_dir"`
}

// Intervals 控制整套节奏。全部基于绝对时间戳判定。
type Intervals struct {
	PackInterval    Duration `yaml:"pack_interval"`
	CheckinInterval Duration `yaml:"checkin_interval"`
	MissThreshold   int      `yaml:"miss_threshold"`
	FinalGrace      Duration `yaml:"final_grace"`
	PasswordDelay   Duration `yaml:"password_delay"`
	FileDelay       Duration `yaml:"file_delay"`
}

type Archive struct {
	KeepArchives        int    `yaml:"keep_archives"`
	PasswordLength      int    `yaml:"password_length"`
	LargeFileThreshold  Bytes  `yaml:"large_file_threshold"`
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
	HeartbeatEnabled  bool     `yaml:"heartbeat_enabled"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
}

type Logging struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// Templates 是单一语言的全部文案模板。占位符用 {name}、{password}、{url} 等。
type Templates struct {
	CheckinTelegram      string `yaml:"checkin_telegram"`
	FinalWarningTelegram string `yaml:"final_warning_telegram"`
	WarnEmailSubject     string `yaml:"warn_email_subject"`
	WarnEmailBody        string `yaml:"warn_email_body"`
	PasswordEmailSubject string `yaml:"password_email_subject"`
	PasswordEmailBody    string `yaml:"password_email_body"`
	FileEmailSubject     string `yaml:"file_email_subject"`
	FileEmailBodyAttach  string `yaml:"file_email_body_attach"`
	FileEmailBodyLink    string `yaml:"file_email_body_link"`
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
	if c.Download.Mode == "" {
		c.Download.Mode = "self_hosted"
	}
	if c.Download.MaxDownloads == 0 {
		c.Download.MaxDownloads = 5
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
	if c.Intervals.CheckinInterval.Std() <= 0 {
		errs = append(errs, "intervals.checkin_interval 必须为正")
	}
	if c.Intervals.PackInterval.Std() <= 0 {
		errs = append(errs, "intervals.pack_interval 必须为正")
	}
	if c.Intervals.MissThreshold < 1 {
		errs = append(errs, "intervals.miss_threshold 必须 >= 1")
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
	if c.SMTP.FromEmail == "" {
		errs = append(errs, "smtp.from_email 不能为空")
	}
	if len(c.Beneficiaries) == 0 {
		errs = append(errs, "beneficiaries 至少需要一个受益人")
	}
	for i, b := range c.Beneficiaries {
		if b.Email == "" {
			errs = append(errs, fmt.Sprintf("beneficiaries[%d].email 不能为空", i))
		}
		if b.Lang == "" {
			c.Beneficiaries[i].Lang = "zh" // 默认中文
		}
	}
	if c.Download.Mode != "self_hosted" && c.Download.Mode != "s3" {
		errs = append(errs, "download.mode 必须为 self_hosted 或 s3")
	}
	if c.StateProtection.EncryptPasswordField && c.StateProtection.MasterPassphrase == "" {
		errs = append(errs, "启用 state_protection.encrypt_password_field 时 master_passphrase 不能为空")
	}
	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
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
