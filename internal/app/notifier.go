// Package app 把 telegram、mailer、packer、templates 粘合为 scheduler 所需的
// Notifier / Packer 实现，并提供主运行循环。
package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/download"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/packer"
	"github.com/ofilm/if-i-am-gone/internal/state"
	"github.com/ofilm/if-i-am-gone/internal/telegram"
	"github.com/ofilm/if-i-am-gone/internal/templates"
)

// Notifier 实现 scheduler.Notifier，整合 Telegram 与 Email。
type Notifier struct {
	cfg  *config.Config
	bot  telegramClient
	mail mailClient
	dl   *download.Service
	logf func(string, ...any)
}

type telegramClient interface {
	SendCheckin(text, buttonText, token string) error
	SendMessage(text string) error
}

type mailClient interface {
	Send(msg mailer.Message) error
}

func NewNotifier(cfg *config.Config, bot *telegram.Bot, mail *mailer.Mailer, dl *download.Service, logf func(string, ...any)) *Notifier {
	return &Notifier{cfg: cfg, bot: bot, mail: mail, dl: dl, logf: logf}
}

func newNotifierForTest(cfg *config.Config, bot telegramClient, mail mailClient, dl *download.Service) *Notifier {
	return &Notifier{cfg: cfg, bot: bot, mail: mail, dl: dl, logf: func(string, ...any) {}}
}

func (n *Notifier) SendCheckin(token string) error {
	t := n.cfg.Lang("zh")
	text := t.CheckinTelegram
	if text == "" {
		text = "本月安全确认：如果你一切正常，请点击下方按钮完成确认。"
	}
	buttonText := t.CheckinButtonText
	if buttonText == "" {
		buttonText = "确认正常"
	}
	return n.bot.SendCheckin(text, buttonText, token)
}

func (n *Notifier) SendDailyReminder(day int, isLast bool) error {
	t := n.cfg.Lang("zh")
	text := t.DailyReminderTelegram
	if isLast && t.FinalReminderTelegram != "" {
		text = t.FinalReminderTelegram
	}
	if text == "" && isLast {
		text = "安全确认提醒：这是本轮连续提醒的最后一天。\n如果你一切正常，请尽快点击最新确认消息中的“确认正常”按钮。若仍未确认，系统将进入预设通知流程。"
	}
	if text == "" {
		text = "安全确认提醒：系统已<N>天没收到你的确认。\n如果你一切正常，请点击最新确认消息中的“确认正常”按钮。"
	}
	return n.bot.SendMessage(templates.Render(text, map[string]string{
		"N": fmt.Sprintf("%d", day),
	}))
}

func (n *Notifier) SendStageReminder(stage state.Stage) error {
	t := n.cfg.Lang("zh")
	text := ""
	switch stage {
	case state.StageWarn:
		text = t.WarnStageTelegram
		if text == "" {
			text = "阶段提醒：系统即将向受益人发送预提醒邮件。\n如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。"
		}
	case state.StagePassword:
		text = t.PasswordStageTelegram
		if text == "" {
			text = "阶段提醒：系统即将打包文件，并向受益人发送解压密码。\n如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。"
		}
	case state.StageFile:
		text = t.FileStageTelegram
		if text == "" {
			text = "阶段提醒：系统即将向受益人发送加密文件下载链接。\n如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。"
		}
	default:
		text = "阶段提醒：系统即将进入下一步预设通知流程。如果你一切正常，请点击最新确认消息暂停后续流程。"
	}
	return n.bot.SendMessage(text)
}

func (n *Notifier) SendHeartbeat() error {
	t := n.cfg.Lang("zh")
	text := t.HeartbeatTelegram
	if text == "" {
		text = "系统巡检正常：服务正在按计划运行。若长期收不到此消息，请检查服务器是否在线。"
	}
	return n.bot.SendMessage(text)
}

func (n *Notifier) SendMessageSafe(text string) error {
	t := n.cfg.Lang("zh")
	if text == "" && t.CancelFlowTelegram != "" {
		text = t.CancelFlowTelegram
	}
	return n.bot.SendMessage(text)
}

func (n *Notifier) DeliverWarn(b config.Beneficiary, passwordSendDate, fileLinkSendDate time.Time) error {
	t := n.cfg.Lang(b.Lang)
	vars := map[string]string{
		"name":                b.Name,
		"owner":               n.cfg.SMTP.FromName,
		"password_delay_text": durationText(n.cfg.TargetFlow.PasswordDelayAfterWarn.Std()),
		"file_delay_text":     durationText(n.cfg.TargetFlow.FileDelayAfterPassword.Std()),
		"password_send_date":  n.formatDateTime(passwordSendDate),
		"file_link_send_date": n.formatDateTime(fileLinkSendDate),
	}
	return n.mail.Send(mailer.Message{
		To:      b.Email,
		ToName:  b.Name,
		Subject: templates.Render(t.WarnEmailSubject, vars),
		Body:    templates.Render(t.WarnEmailBody, vars),
	})
}

func (n *Notifier) DeliverPassword(b config.Beneficiary, password string, fileLinkSendDate time.Time) error {
	t := n.cfg.Lang(b.Lang)
	vars := map[string]string{
		"name":                b.Name,
		"password":            password,
		"owner":               n.cfg.SMTP.FromName,
		"file_delay_text":     durationText(n.cfg.TargetFlow.FileDelayAfterPassword.Std()),
		"file_link_send_date": n.formatDateTime(fileLinkSendDate),
	}
	return n.mail.Send(mailer.Message{
		To:      b.Email,
		ToName:  b.Name,
		Subject: templates.Render(t.PasswordEmailSubject, vars),
		Body:    templates.Render(t.PasswordEmailBody, vars),
	})
}

func (n *Notifier) DeliverFile(b config.Beneficiary, archivePath, password string) error {
	t := n.cfg.Lang(b.Lang)
	if n.dl == nil {
		return fmt.Errorf("下载链接服务未初始化")
	}
	link, expiresAt, maxDownloads, err := n.dl.CreateLink(archivePath, b.Email)
	if err != nil {
		return err
	}
	vars := map[string]string{
		"name":          b.Name,
		"owner":         n.cfg.SMTP.FromName,
		"url":           link,
		"expiry":        n.formatDateTime(expiresAt),
		"max_downloads": downloadLimitText(maxDownloads),
	}
	return n.mail.Send(mailer.Message{
		To:      b.Email,
		ToName:  b.Name,
		Subject: templates.Render(t.FileEmailSubject, vars),
		Body:    templates.Render(t.FileEmailBodyLink, vars),
	})
}

func downloadLimitText(maxDownloads int) string {
	if maxDownloads <= 0 {
		return "不限制"
	}
	return fmt.Sprintf("%d", maxDownloads)
}

func durationText(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%.0f 天", d.Hours()/24)
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%.0f 小时", d.Hours())
	}
	return d.String()
}

func (n *Notifier) formatDateTime(t time.Time) string {
	loc, err := time.LoadLocation(n.cfg.TargetFlow.Timezone)
	if err != nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04 MST")
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
