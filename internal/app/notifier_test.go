package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/download"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

type fakeBot struct {
	checkinText  string
	buttonText   string
	checkinToken string
	messages     []string
}

func (f *fakeBot) SendCheckin(text, buttonText, token string) error {
	f.checkinText = text
	f.buttonText = buttonText
	f.checkinToken = token
	return nil
}

func (f *fakeBot) SendMessage(text string) error {
	f.messages = append(f.messages, text)
	return nil
}

type fakeMail struct {
	messages []mailer.Message
}

func (f *fakeMail) Send(msg mailer.Message) error {
	f.messages = append(f.messages, msg)
	return nil
}

type appMemoryStore struct {
	tokens map[string]state.DownloadToken
}

func newAppMemoryStore() *appMemoryStore {
	return &appMemoryStore{tokens: map[string]state.DownloadToken{}}
}

func (m *appMemoryStore) CreateDownloadToken(t state.DownloadToken) error {
	m.tokens[t.Token] = t
	return nil
}

func (m *appMemoryStore) GetDownloadToken(token string) (*state.DownloadToken, error) {
	return nil, nil
}
func (m *appMemoryStore) IncrementDownloadCount(token string) error      { return nil }
func (m *appMemoryStore) Audit(event, detail string, at time.Time) error { return nil }

func testNotifierConfig() *config.Config {
	return &config.Config{
		SMTP: config.SMTP{FromName: "主人"},
		TargetFlow: config.TargetFlow{
			PasswordDelayAfterWarn: config.Duration(72 * time.Hour),
			FileDelayAfterPassword: config.Duration(168 * time.Hour),
			Timezone:               "UTC",
		},
		Download: config.Download{
			Mode:         configDownloadSelfHosted,
			LinkExpiry:   config.Duration(24 * time.Hour),
			MaxDownloads: 3,
			SelfHosted:   config.SelfHostedConfig{PublicBaseURL: "https://example.com"},
		},
		Templates: map[string]config.Templates{
			"zh": {
				CheckinTelegram:       "确认文本",
				CheckinButtonText:     "按钮文本",
				CheckinAcceptedReply:  "确认成功",
				CheckinExpiredReply:   "确认过期",
				CheckinErrorReply:     "确认出错",
				DailyReminderTelegram: "第<N>天提醒",
				FinalReminderTelegram: "最后第<N>天提醒",
				WarnStageTelegram:     "预提醒阶段",
				PasswordStageTelegram: "密码阶段",
				FileStageTelegram:     "文件阶段",
				CancelFlowTelegram:    "取消流程",
				HeartbeatTelegram:     "心跳文本",
				WarnEmailSubject:      "预提醒 {name}",
				WarnEmailBody:         "{owner} {password_delay_text} {password_send_date} {file_delay_text} {file_link_send_date}",
				PasswordEmailSubject:  "密码",
				PasswordEmailBody:     "{name} {password} {file_delay_text} {file_link_send_date}",
				FileEmailSubject:      "下载",
				FileEmailBodyLink:     "{name} {url} {expiry} {max_downloads}",
			},
		},
	}
}

const configDownloadSelfHosted = "self_hosted"

func TestNotifierTelegramTemplates(t *testing.T) {
	cfg := testNotifierConfig()
	bot := &fakeBot{}
	mail := &fakeMail{}
	n := newNotifierForTest(cfg, bot, mail, nil)

	if err := n.SendCheckin("tok"); err != nil {
		t.Fatal(err)
	}
	if bot.checkinText != "确认文本" || bot.buttonText != "按钮文本" || bot.checkinToken != "tok" {
		t.Fatalf("checkin 不对: text=%q button=%q token=%q", bot.checkinText, bot.buttonText, bot.checkinToken)
	}
	if err := n.SendReminder(3, false); err != nil {
		t.Fatal(err)
	}
	if err := n.SendReminder(7, true); err != nil {
		t.Fatal(err)
	}
	if err := n.SendStageReminder(state.StagePassword); err != nil {
		t.Fatal(err)
	}
	if err := n.SendHeartbeat(); err != nil {
		t.Fatal(err)
	}
	if bot.messages[0] != "第3天提醒" || bot.messages[1] != "最后第7天提醒" || bot.messages[2] != "密码阶段" || bot.messages[3] != "心跳文本" {
		t.Fatalf("消息不对: %+v", bot.messages)
	}
}

func TestNotifierEmailTemplates(t *testing.T) {
	cfg := testNotifierConfig()
	bot := &fakeBot{}
	mail := &fakeMail{}
	n := newNotifierForTest(cfg, bot, mail, nil)
	b := config.Beneficiary{Name: "张三", Email: "a@example.com", Lang: "zh"}
	passwordDate := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	fileDate := time.Date(2026, 1, 9, 8, 0, 0, 0, time.UTC)

	if err := n.DeliverWarn(b, passwordDate, fileDate); err != nil {
		t.Fatal(err)
	}
	if err := n.DeliverPassword(b, "secret", fileDate); err != nil {
		t.Fatal(err)
	}

	if len(mail.messages) != 2 {
		t.Fatalf("邮件数量=%d", len(mail.messages))
	}
	warn := mail.messages[0]
	if warn.Subject != "预提醒 张三" || !strings.Contains(warn.Body, "主人 3 天 2026-01-02 08:00 UTC 7 天 2026-01-09 08:00 UTC") {
		t.Fatalf("预提醒邮件不对: %+v", warn)
	}
	password := mail.messages[1]
	if !strings.Contains(password.Body, "张三 secret 7 天 2026-01-09 08:00 UTC") {
		t.Fatalf("密码邮件不对: %+v", password)
	}
}

func TestNotifierDeliverFileUsesDownloadLinkWithoutAttachment(t *testing.T) {
	cfg := testNotifierConfig()
	store := newAppMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dl := download.NewServiceForTest(cfg, store, func() (string, error) { return "tok-file", nil }, func() time.Time { return now })
	bot := &fakeBot{}
	mail := &fakeMail{}
	n := newNotifierForTest(cfg, bot, mail, dl)

	err := n.DeliverFile(config.Beneficiary{Name: "张三", Email: "a@example.com", Lang: "zh"}, "/tmp/archive.zip", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(mail.messages) != 1 {
		t.Fatalf("邮件数量=%d", len(mail.messages))
	}
	msg := mail.messages[0]
	if len(msg.Attachments) != 0 {
		t.Fatalf("下载链接邮件不应带附件: %+v", msg.Attachments)
	}
	if !strings.Contains(msg.Body, "https://example.com/download/tok-file") || !strings.Contains(msg.Body, "3") {
		t.Fatalf("下载邮件正文不对: %q", msg.Body)
	}
	if store.tokens["tok-file"].ArchivePath != "/tmp/archive.zip" {
		t.Fatalf("token 未记录归档路径: %+v", store.tokens["tok-file"])
	}
}

type appFakeS3Linker struct {
	url       string
	expiresAt time.Time
}

func (f appFakeS3Linker) UploadAndPresign(ctx context.Context, archivePath string, expires time.Duration) (string, time.Time, error) {
	return f.url, f.expiresAt, nil
}

func TestNotifierDeliverFileUsesS3PresignedLink(t *testing.T) {
	cfg := testNotifierConfig()
	cfg.Download.Mode = "s3"
	cfg.Download.S3 = config.S3Config{
		Endpoint:      "https://s3.example.com",
		Bucket:        "ifgone",
		Region:        "us-east-1",
		AccessKey:     "key",
		SecretKey:     "secret",
		PresignExpiry: config.Duration(12 * time.Hour),
	}
	store := newAppMemoryStore()
	dl := download.NewServiceWithS3ForTest(cfg, store, appFakeS3Linker{
		url:       "https://s3.example.com/ifgone/archive.zip?sig=ok",
		expiresAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}, func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	mail := &fakeMail{}
	n := newNotifierForTest(cfg, &fakeBot{}, mail, dl)

	err := n.DeliverFile(config.Beneficiary{Name: "张三", Email: "a@example.com", Lang: "zh"}, "/tmp/archive.zip", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(mail.messages) != 1 {
		t.Fatalf("邮件数量=%d", len(mail.messages))
	}
	msg := mail.messages[0]
	if len(msg.Attachments) != 0 {
		t.Fatalf("S3 下载链接邮件不应带附件: %+v", msg.Attachments)
	}
	if !strings.Contains(msg.Body, "https://s3.example.com/ifgone/archive.zip?sig=ok") {
		t.Fatalf("下载邮件正文缺少 S3 链接: %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "不限制") {
		t.Fatalf("S3 下载邮件应展示不限制下载次数: %q", msg.Body)
	}
	if len(store.tokens) != 0 {
		t.Fatalf("S3 模式不应创建本地下载 token: %+v", store.tokens)
	}
}
