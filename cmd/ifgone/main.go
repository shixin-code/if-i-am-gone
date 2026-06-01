// Command ifgone 是「意外开关」(if-i-am-gone) 的主程序入口。
//
// 它并发运行两个循环：
//   - tick 循环：每分钟读取状态推导动作（打包、发确认、触发流程）；
//   - Telegram polling：接收用户确认，立即重置状态。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/app"
	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/scheduler"
	"github.com/ofilm/if-i-am-gone/internal/state"
	"github.com/ofilm/if-i-am-gone/internal/telegram"
)

func main() {
	configPath := flag.String("config", "/app/config.yaml", "配置文件路径")
	tickInterval := flag.Duration("tick", time.Minute, "tick 周期")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	logf := setupLogger(cfg)
	logf("if-i-am-gone 启动，配置=%s", *configPath)

	// state 目录与数据库
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		log.Fatalf("创建状态目录失败: %v", err)
	}
	dbPath := filepath.Join(cfg.StateDir, "state.db")
	store, err := state.Open(dbPath)
	if err != nil {
		log.Fatalf("打开状态库失败: %v", err)
	}
	defer store.Close()

	// 依赖装配
	bot := telegram.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	mail := &mailer.Mailer{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, UseSSL: cfg.SMTP.UseSSL,
		Username: cfg.SMTP.Username, Password: cfg.SMTP.Password,
		FromName: cfg.SMTP.FromName, FromEmail: cfg.SMTP.FromEmail,
	}
	notifier := app.NewNotifier(cfg, bot, mail, logf)
	pk := app.NewPackerAdapter(cfg)
	sched := scheduler.New(cfg, store, notifier, pk, genToken, logf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Telegram 确认 polling
	go bot.PollConfirmations(ctx,
		func(ev telegram.ConfirmEvent) (bool, string) {
			accepted, err := sched.Confirm(time.Now().UTC(), ev.Token)
			if err != nil {
				logf("处理确认出错: %v", err)
				return false, "处理出错，请稍后再试"
			}
			if accepted {
				return true, "已确认，计时器已重置 ✅"
			}
			return false, "此确认已过期，请用最新的确认消息"
		},
		func(chatID int64, data string) {
			logf("收到来自非授权 chat_id=%d 的回调，已忽略 (data=%s)", chatID, data)
			_ = store.Audit("spoof_attempt", "chatID 不匹配", time.Now().UTC())
		},
	)

	// tick 循环：启动先跑一拍，之后按周期。
	ticker := time.NewTicker(*tickInterval)
	defer ticker.Stop()
	runTick(sched, logf)
	for {
		select {
		case <-ctx.Done():
			logf("收到退出信号，正在关闭…")
			return
		case <-ticker.C:
			runTick(sched, logf)
		}
	}
}

// runTick 跑一拍 tick，用 recover 包裹保证单次失败不致命。
func runTick(sched *scheduler.Scheduler, logf func(string, ...any)) {
	defer func() {
		if r := recover(); r != nil {
			logf("tick panic 已恢复: %v", r)
		}
	}()
	if err := sched.Tick(time.Now().UTC()); err != nil {
		logf("tick 出错: %v", err)
	}
}

// genToken 生成 16 字节高熵一次性确认 token。
func genToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// setupLogger 返回一个写到 stdout 与（可选）日志文件的 logf。
func setupLogger(cfg *config.Config) func(string, ...any) {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	if cfg.Logging.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Logging.File), 0o700); err == nil {
			if f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				logger = log.New(newMultiWriter(os.Stdout, f), "", log.LstdFlags|log.LUTC)
			}
		}
	}
	return func(format string, args ...any) {
		logger.Printf(format, args...)
	}
}
