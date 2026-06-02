package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/download"
	"github.com/ofilm/if-i-am-gone/internal/scheduler"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

type e2ePacker struct {
	path string
	n    int
}

func (p *e2ePacker) Pack(now time.Time) (string, string, string, error) {
	p.n++
	return p.path, "sha-e2e", "plain-password", nil
}

func e2eConfig(stateDir string) *config.Config {
	cfg := testNotifierConfig()
	cfg.StateDir = stateDir
	cfg.SourceDir = filepath.Join(stateDir, "source")
	cfg.TargetFlow.CheckinDayOfMonth = 1
	cfg.TargetFlow.DailyReminderDays = 1
	cfg.TargetFlow.PasswordDelayAfterWarn = config.Duration(time.Minute)
	cfg.TargetFlow.FileDelayAfterPassword = config.Duration(time.Minute)
	cfg.Download.LinkExpiry = config.Duration(time.Hour)
	cfg.Download.MaxDownloads = 2
	cfg.StateProtection.EncryptPasswordField = true
	cfg.StateProtection.MasterPassphrase = "master-passphrase"
	cfg.Beneficiaries = []config.Beneficiary{{Name: "张三", Email: "a@example.com", Lang: "zh"}}
	return cfg
}

func TestTargetFlowEndToEndSimulation(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := e2eConfig(dir)
	archivePath := filepath.Join(dir, "archive.zip")
	packer := &e2ePacker{path: archivePath}
	bot := &fakeBot{}
	mail := &fakeMail{}
	nowForDownload := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tokenSeq := 0
	dl := download.NewServiceForTest(cfg, store, func() (string, error) {
		tokenSeq++
		return "download-token", nil
	}, func() time.Time { return nowForDownload })
	notifier := newNotifierForTest(cfg, bot, mail, dl)
	s := scheduler.New(cfg, store, notifier, packer, func() (string, error) { return "confirm-token", nil }, nil)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st, _ := store.Load()
	st.LastConfirmedAt = ptr(base.Add(-time.Hour))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	if err := s.Tick(base); err != nil {
		t.Fatal(err)
	}
	if bot.checkinToken != "confirm-token" {
		t.Fatalf("应发送确认 token，实际 %q", bot.checkinToken)
	}

	if err := s.Tick(base.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(bot.messages) == 0 || !strings.Contains(bot.messages[0], "最后第1天提醒") {
		t.Fatalf("应发送最后连续提醒，messages=%+v", bot.messages)
	}

	if err := s.Tick(base.Add(48*time.Hour + time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("应进入 PENDING_TRIGGER，实际 %s", st.Phase)
	}

	if err := s.Tick(base.Add(48*time.Hour + 2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseWarned || len(mail.messages) != 1 {
		t.Fatalf("预提醒后状态/邮件不对: phase=%s mails=%d", st.Phase, len(mail.messages))
	}
	if mail.messages[0].Subject != "预提醒 张三" {
		t.Fatalf("预提醒主题=%q", mail.messages[0].Subject)
	}

	if err := s.Tick(base.Add(48*time.Hour + 4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhasePasswordSent || len(mail.messages) != 2 {
		t.Fatalf("密码阶段后状态/邮件不对: phase=%s mails=%d", st.Phase, len(mail.messages))
	}
	if packer.n != 1 {
		t.Fatalf("密码阶段应打包一次，实际 %d", packer.n)
	}
	if st.CurrentArchivePassword == "plain-password" {
		t.Fatal("state 中归档密码应加密保存")
	}
	if !strings.Contains(mail.messages[1].Body, "plain-password") {
		t.Fatalf("密码邮件应包含明文密码: %s", mail.messages[1].Body)
	}

	if err := s.Tick(base.Add(48*time.Hour + 6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseFileSent || len(mail.messages) != 3 {
		t.Fatalf("文件阶段后状态/邮件不对: phase=%s mails=%d", st.Phase, len(mail.messages))
	}
	if len(mail.messages[2].Attachments) != 0 || !strings.Contains(mail.messages[2].Body, "https://example.com/download/download-token") {
		t.Fatalf("下载链接邮件不对: %+v", mail.messages[2])
	}
	dt, err := store.GetDownloadToken("download-token")
	if err != nil {
		t.Fatal(err)
	}
	if dt == nil || dt.ArchivePath != archivePath || dt.Beneficiary != "a@example.com" || dt.MaxDownloads != 2 {
		t.Fatalf("download token 不对: %+v", dt)
	}

	if err := s.Tick(base.Add(48*time.Hour + 7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseCompleted {
		t.Fatalf("终态应为 COMPLETED，实际 %s", st.Phase)
	}
	if tokenSeq != 1 {
		t.Fatalf("下载链接 token 应只生成一次，实际 %d", tokenSeq)
	}
}

func TestTargetFlowEndToEndCancelBeforePassword(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := e2eConfig(dir)
	packer := &e2ePacker{path: filepath.Join(dir, "archive.zip")}
	bot := &fakeBot{}
	mail := &fakeMail{}
	dl := download.NewServiceForTest(cfg, store, func() (string, error) { return "download-token", nil }, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	s := scheduler.New(cfg, store, newNotifierForTest(cfg, bot, mail, dl), packer, func() (string, error) { return "confirm-token", nil }, nil)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st, _ := store.Load()
	st.LastConfirmedAt = ptr(base.Add(-time.Hour))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	_ = s.Tick(base)
	_ = s.Tick(base.Add(24 * time.Hour))
	_ = s.Tick(base.Add(48*time.Hour + time.Minute))
	_ = s.Tick(base.Add(48*time.Hour + 2*time.Minute))
	st, _ = store.Load()
	if st.Phase != state.PhaseWarned {
		t.Fatalf("应处于 WARNED，实际 %s", st.Phase)
	}

	accepted, err := s.Confirm(base.Add(48*time.Hour+3*time.Minute), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("确认取消应成功 accepted=%v err=%v", accepted, err)
	}
	if err := s.Tick(base.Add(48*time.Hour + 4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseAlive {
		t.Fatalf("取消后应回 ALIVE，实际 %s", st.Phase)
	}
	if packer.n != 0 {
		t.Fatalf("取消后不应进入密码阶段打包，packs=%d", packer.n)
	}
	if len(mail.messages) != 1 {
		t.Fatalf("取消后只应有预提醒邮件，实际 %d", len(mail.messages))
	}
}

func ptr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}
