// Command ifgonectl 提供 if-i-am-gone 的本地管理工具。
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/packer"
	"github.com/ofilm/if-i-am-gone/internal/secretbox"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

var sendTestEmail = func(cfg *config.Config, msg mailer.Message) error {
	mail := &mailer.Mailer{
		Host:      cfg.SMTP.Host,
		Port:      cfg.SMTP.Port,
		UseSSL:    cfg.SMTP.UseSSL,
		Username:  cfg.SMTP.Username,
		Password:  cfg.SMTP.Password,
		FromName:  cfg.SMTP.FromName,
		FromEmail: cfg.SMTP.FromEmail,
	}
	return mail.Send(msg)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		printUsage(out)
		return nil
	}
	switch args[0] {
	case "status":
		return runStatus(args[1:], out)
	case "dry-run":
		return runDryRun(args[1:], out)
	case "cleanup-tokens":
		return runCleanupTokens(args[1:], out)
	case "test-email":
		return runTestEmail(args[1:], out)
	case "pack":
		return runPack(args[1:], out)
	case "drill":
		return runDrill(args[1:], out)
	case "-h", "--help", "help":
		printUsage(out)
		return nil
	default:
		return fmt.Errorf("未知命令: %s", args[0])
	}
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `用法:
  ifgonectl status --config config.yaml
  ifgonectl dry-run --config config.yaml
  ifgonectl cleanup-tokens --config config.yaml
  ifgonectl test-email --config config.yaml [--to you@example.com]
  ifgonectl pack --config config.yaml [--save-state]
  ifgonectl drill advance-checkin --config config.yaml --days 2

命令:
  status          查看当前 state.db 状态摘要
  dry-run         根据当前状态给出下一步动作提示，不发送消息、不打包
  cleanup-tokens  清理已过期下载 token
  test-email      发送一封 SMTP 测试邮件
  pack            手动打包 source_dir；加 --save-state 可写入当前 state
  drill           测试演练辅助命令，只调整 state 时间戳，不发送消息、不打包`)
}

func loadConfigAndStore(args []string, command string) (*config.Config, *state.Store, func() error, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return nil, nil, nil, err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.ValidateRuntimePaths(); err != nil {
		return nil, nil, nil, err
	}
	store, err := state.Open(filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, store, store.Close, nil
}

func runStatus(args []string, out io.Writer) error {
	_, store, closeStore, err := loadConfigAndStore(args, "status")
	if err != nil {
		return err
	}
	defer closeStore()
	st, err := store.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "phase: %s\n", st.Phase)
	fmt.Fprintf(out, "miss_count: %d\n", st.MissCount)
	fmt.Fprintf(out, "pending_token: %s\n", maskPresence(st.PendingToken))
	fmt.Fprintf(out, "last_confirmed_at: %s\n", formatTime(st.LastConfirmedAt))
	fmt.Fprintf(out, "last_checkin_sent_at: %s\n", formatTime(st.LastCheckinSentAt))
	fmt.Fprintf(out, "warned_at: %s\n", formatTime(st.WarnedAt))
	fmt.Fprintf(out, "password_sent_at: %s\n", formatTime(st.PasswordSentAt))
	fmt.Fprintf(out, "file_sent_at: %s\n", formatTime(st.FileSentAt))
	fmt.Fprintf(out, "archive_path: %s\n", emptyDash(st.CurrentArchivePath))
	fmt.Fprintf(out, "archive_sha256: %s\n", emptyDash(st.CurrentArchiveSHA256))
	fmt.Fprintf(out, "archive_password: %s\n", maskPresence(st.CurrentArchivePassword))
	return nil
}

func runDryRun(args []string, out io.Writer) error {
	cfg, store, closeStore, err := loadConfigAndStore(args, "dry-run")
	if err != nil {
		return err
	}
	defer closeStore()
	st, err := store.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "phase: %s\n", st.Phase)
	fmt.Fprintf(out, "next_action: %s\n", describeNextAction(cfg, st, time.Now().UTC()))
	return nil
}

func runCleanupTokens(args []string, out io.Writer) error {
	_, store, closeStore, err := loadConfigAndStore(args, "cleanup-tokens")
	if err != nil {
		return err
	}
	defer closeStore()
	if err := store.CleanupExpiredTokens(time.Now().UTC()); err != nil {
		return err
	}
	fmt.Fprintln(out, "cleanup_expired_tokens: ok")
	return nil
}

func runTestEmail(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("test-email", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "配置文件路径")
	to := fs.String("to", "", "测试邮件收件人，默认使用 smtp.from_email")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateRuntimePaths(); err != nil {
		return err
	}
	recipient := *to
	if recipient == "" {
		recipient = cfg.SMTP.FromEmail
	}
	msg := mailer.Message{
		To:      recipient,
		ToName:  "ifgonectl",
		Subject: "[if-i-am-gone] SMTP 测试邮件",
		Body:    fmt.Sprintf("这是一封 ifgonectl 测试邮件。\n\n发送时间：%s\n配置来源：%s\n", time.Now().UTC().Format(time.RFC3339), *configPath),
	}
	if err := sendTestEmail(cfg, msg); err != nil {
		return err
	}
	fmt.Fprintf(out, "test_email_sent: %s\n", recipient)
	return nil
}

func runPack(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "配置文件路径")
	saveState := fs.Bool("save-state", false, "把本次打包结果写入 state.db")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateRuntimePaths(); err != nil {
		return err
	}
	res, err := packer.Build(cfg.SourceDir, filepath.Join(cfg.StateDir, "archives"), cfg.Archive.PasswordLength, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := packer.CleanupOld(filepath.Join(cfg.StateDir, "archives"), cfg.Archive.KeepArchives); err != nil {
		return err
	}
	fmt.Fprintf(out, "archive_path: %s\n", res.Path)
	fmt.Fprintf(out, "archive_sha256: %s\n", res.SHA256)
	fmt.Fprintf(out, "archive_size: %d\n", res.Size)
	fmt.Fprintf(out, "password: %s\n", res.Password)
	if *saveState {
		store, err := state.Open(filepath.Join(cfg.StateDir, "state.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		st, err := store.Load()
		if err != nil {
			return err
		}
		password := res.Password
		if cfg.StateProtection.EncryptPasswordField {
			encrypted, err := secretbox.Encrypt(password, cfg.StateProtection.MasterPassphrase)
			if err != nil {
				return err
			}
			password = encrypted
		}
		now := time.Now().UTC()
		st.CurrentArchivePath = res.Path
		st.CurrentArchiveSHA256 = res.SHA256
		st.CurrentArchivePassword = password
		st.LastPackAt = &now
		if err := store.Save(st); err != nil {
			return err
		}
		if err := store.Audit("manual_pack", res.SHA256, now); err != nil {
			return err
		}
		fmt.Fprintln(out, "state_saved: true")
	}
	return nil
}

func runDrill(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("drill 需要子命令，例如 advance-checkin")
	}
	switch args[0] {
	case "advance-checkin":
		return runDrillAdvanceCheckin(args[1:], out)
	default:
		return fmt.Errorf("未知 drill 子命令: %s", args[0])
	}
}

func runDrillAdvanceCheckin(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("drill advance-checkin", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "配置文件路径")
	days := fs.Int("days", 2, "把 last_checkin_sent_at 调整为当前时间之前的天数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *days < 1 {
		return fmt.Errorf("--days 必须 >= 1")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateRuntimePaths(); err != nil {
		return err
	}
	store, err := state.Open(filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	st, err := store.Load()
	if err != nil {
		return err
	}
	if st.PendingToken == "" || st.LastCheckinSentAt == nil {
		return fmt.Errorf("当前没有待确认的 Telegram token，请先运行主程序发送 D0 安全确认")
	}
	if st.Phase != state.PhaseAlive && st.Phase != state.PhaseGrace {
		return fmt.Errorf("当前阶段 %s 不适合 advance-checkin，请仅在 D0/连续提醒演练阶段使用", st.Phase)
	}
	now := time.Now().UTC()
	shifted := now.Add(-time.Duration(*days) * 24 * time.Hour)
	st.LastCheckinSentAt = &shifted
	if st.MissCount > *days {
		st.MissCount = *days
	}
	if err := store.Save(st); err != nil {
		return err
	}
	if err := store.Audit("drill_advance_checkin", fmt.Sprintf("days=%d", *days), now); err != nil {
		return err
	}
	fmt.Fprintf(out, "drill_advance_checkin: ok\n")
	fmt.Fprintf(out, "last_checkin_sent_at: %s\n", shifted.Format(time.RFC3339))
	fmt.Fprintf(out, "next_hint: run ifgone tick or wait for the next tick\n")
	return nil
}

func describeNextAction(cfg *config.Config, st *state.State, now time.Time) string {
	switch st.Phase {
	case state.PhaseAlive:
		if st.PendingToken != "" {
			return "等待用户点击最新 Telegram 确认按钮"
		}
		return fmt.Sprintf("等待每月 %d 号发送安全确认", cfg.TargetFlow.CheckinDayOfMonth)
	case state.PhaseGrace:
		return "处于连续提醒期，下一次 tick 会按天数发送 Telegram 提醒或进入预提醒阶段"
	case state.PhasePendingTrigger:
		return "下一次 tick 会先发送用户阶段提醒，再向受益人发送预提醒邮件"
	case state.PhaseWarned:
		return fmt.Sprintf("等待密码阶段到期，目标时间约为 %s", dueText(st.WarnedAt, cfg.TargetFlow.PasswordDelayAfterWarn.Std(), now))
	case state.PhasePasswordSent:
		return fmt.Sprintf("等待下载链接阶段到期，目标时间约为 %s", dueText(st.PasswordSentAt, cfg.TargetFlow.FileDelayAfterPassword.Std(), now))
	case state.PhaseFileSent:
		return "下一次 tick 会进入 COMPLETED 并清理过期下载 token"
	case state.PhaseCompleted:
		return "流程已完成，等待人工检查或下次重置"
	default:
		return "未知状态，建议检查 state.db 或启动主程序执行兼容归一化"
	}
}

func dueText(t *time.Time, delay time.Duration, now time.Time) string {
	if t == nil {
		return "立即"
	}
	due := t.Add(delay)
	if !now.Before(due) {
		return "立即"
	}
	return due.Format(time.RFC3339)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func maskPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return "<set>"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
