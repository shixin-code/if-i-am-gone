// Package app 把 telegram、mailer、packer、templates 粘合为 scheduler 所需的
// Notifier / Packer 实现，并提供主运行循环。
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/packer"
	"github.com/ofilm/if-i-am-gone/internal/telegram"
	"github.com/ofilm/if-i-am-gone/internal/templates"
)

// Notifier 实现 scheduler.Notifier，整合 Telegram 与 Email。
type Notifier struct {
	cfg  *config.Config
	bot  *telegram.Bot
	mail *mailer.Mailer
	logf func(string, ...any)
}

func NewNotifier(cfg *config.Config, bot *telegram.Bot, mail *mailer.Mailer, logf func(string, ...any)) *Notifier {
	return &Notifier{cfg: cfg, bot: bot, mail: mail, logf: logf}
}

func (n *Notifier) SendCheckin(token string) error {
	t := n.cfg.Lang("zh")
	text := t.CheckinTelegram
	if text == "" {
		text = "定期安全确认：如果你一切正常，请点击下方按钮完成本轮确认。"
	}
	return n.bot.SendCheckin(text, token)
}

func (n *Notifier) SendFinalWarning() error {
	t := n.cfg.Lang("zh")
	text := t.FinalWarningTelegram
	if text == "" {
		text = "安全确认即将超时：系统已连续多轮未收到确认。若你一切正常，请尽快点击最新确认消息中的按钮，系统会暂停后续流程。"
	}
	return n.bot.SendMessage(text)
}

func (n *Notifier) SendHeartbeat() error {
	return n.bot.SendMessage("系统巡检正常：服务正在按计划运行。若长期收不到此消息，请检查服务器是否在线。")
}

func (n *Notifier) SendMessageSafe(text string) error {
	return n.bot.SendMessage(text)
}

// SendOwnerAlert 进入触发预备时给用户本人发邮件（多通道兜底）。
func (n *Notifier) SendOwnerAlert() error {
	return n.mail.Send(mailer.Message{
		To:      n.cfg.SMTP.FromEmail,
		Subject: "[意外开关] 你已多次未确认 —— 即将启动传递流程",
		Body: "这是来自你的「意外开关」系统的提醒：\n\n" +
			"你已连续多次未通过 Telegram 确认。系统即将开始向你设置的家人传递信息。\n" +
			"如果你看到此邮件且一切正常，请尽快通过 Telegram 确认以取消流程。",
	})
}

func (n *Notifier) DeliverWarn(b config.Beneficiary) error {
	t := n.cfg.Lang(b.Lang)
	vars := map[string]string{
		"name":          b.Name,
		"owner":         n.cfg.SMTP.FromName,
		"password_days": daysOf(n.cfg.Intervals.PasswordDelay.Std()),
		"file_days":     daysOf(n.cfg.Intervals.FileDelay.Std()),
	}
	return n.mail.Send(mailer.Message{
		To:      b.Email,
		ToName:  b.Name,
		Subject: templates.Render(t.WarnEmailSubject, vars),
		Body:    templates.Render(t.WarnEmailBody, vars),
	})
}

func (n *Notifier) DeliverPassword(b config.Beneficiary, password string) error {
	t := n.cfg.Lang(b.Lang)
	vars := map[string]string{"name": b.Name, "password": password, "owner": n.cfg.SMTP.FromName}
	return n.mail.Send(mailer.Message{
		To:      b.Email,
		ToName:  b.Name,
		Subject: templates.Render(t.PasswordEmailSubject, vars),
		Body:    templates.Render(t.PasswordEmailBody, vars),
	})
}

// DeliverFile MVP 阶段只做附件投递；超阈值的大文件返回提示性错误，
// 由迭代2 的下载链接方案接管。
func (n *Notifier) DeliverFile(b config.Beneficiary, archivePath, password string) error {
	t := n.cfg.Lang(b.Lang)
	vars := map[string]string{"name": b.Name, "owner": n.cfg.SMTP.FromName}

	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("读取压缩包失败: %w", err)
	}
	if info.Size() >= n.cfg.Archive.LargeFileThreshold.Int64() {
		// MVP：大文件下载链接尚未实现。仍发一封说明邮件，避免家人收不到任何信息。
		n.logf("警告：压缩包 %d 字节超过附件阈值，MVP 暂不支持下载链接，仅发送说明邮件", info.Size())
		return n.mail.Send(mailer.Message{
			To:      b.Email,
			ToName:  b.Name,
			Subject: templates.Render(t.FileEmailSubject, vars),
			Body:    "文件较大，暂无法通过邮件附件发送。请联系系统管理员获取加密文件。",
		})
	}

	content, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("读取压缩包失败: %w", err)
	}
	return n.mail.Send(mailer.Message{
		To:          b.Email,
		ToName:      b.Name,
		Subject:     templates.Render(t.FileEmailSubject, vars),
		Body:        templates.Render(t.FileEmailBodyAttach, vars),
		Attachments: []mailer.Attachment{{Filename: filepath.Base(archivePath), Content: content}},
	})
}

func daysOf(d time.Duration) string {
	return fmt.Sprintf("%.0f", d.Hours()/24)
}

// PackerAdapter 实现 scheduler.Packer。
type PackerAdapter struct {
	cfg *config.Config
}

func NewPackerAdapter(cfg *config.Config) *PackerAdapter { return &PackerAdapter{cfg: cfg} }

func (p *PackerAdapter) Pack(now time.Time) (path, sha256, password string, err error) {
	destDir := filepath.Join(p.cfg.StateDir, "archives")
	res, err := packer.Build(p.cfg.SourceDir, destDir, p.cfg.Archive.PasswordLength, now)
	if err != nil {
		return "", "", "", err
	}
	if cerr := packer.CleanupOld(destDir, p.cfg.Archive.KeepArchives); cerr != nil {
		// 清理失败不影响打包结果。
		_ = cerr
	}
	return res.Path, res.SHA256, res.Password, nil
}
