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
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/app"
	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/download"
	"github.com/ofilm/if-i-am-gone/internal/mailer"
	"github.com/ofilm/if-i-am-gone/internal/reliability"
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
	if err := cfg.ValidateRuntimePaths(); err != nil {
		log.Fatalf("配置路径不可用: %v", err)
	}

	logf := setupLogger(cfg)
	logf("if-i-am-gone 启动，配置=%s", *configPath)

	// state 目录与数据库
	dbPath := filepath.Join(cfg.StateDir, "state.db")
	store, err := state.Open(dbPath)
	if err != nil {
		log.Fatalf("打开状态库失败: %v", err)
	}
	defer store.Close()
	if result, err := store.NormalizeForTargetFlow(time.Now().UTC()); err != nil {
		log.Fatalf("旧状态库兼容检查失败: %v", err)
	} else if result.Changed {
		logf("旧状态库已按目标流程归一化: %s -> %s (%s)", result.From, result.To, result.Reason)
	}

	// 依赖装配
	bot := telegram.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	mail := &mailer.Mailer{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, UseSSL: cfg.SMTP.UseSSL,
		Username: cfg.SMTP.Username, Password: cfg.SMTP.Password,
		FromName: cfg.SMTP.FromName, FromEmail: cfg.SMTP.FromEmail,
	}
	downloadService := download.NewService(cfg, store)
	notifier := app.NewNotifier(cfg, bot, mail, downloadService, logf)
	pk := app.NewPackerAdapter(cfg)
	sched := scheduler.New(cfg, store, notifier, pk, genToken, logf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.Download.Mode == "self_hosted" {
		startDownloadServer(ctx, cfg, store, logf)
	}
	startHealthcheckRunner(ctx, cfg, store, logf)

	// Telegram 确认 polling
	go bot.PollConfirmations(ctx,
		func(ev telegram.ConfirmEvent) (bool, string) {
			accepted, err := sched.Confirm(time.Now().UTC(), ev.Token)
			if err != nil {
				logf("处理确认出错: %v", err)
				return false, callbackReply(cfg, func(t config.Templates) string { return t.CheckinErrorReply }, "处理出错，请稍后再试")
			}
			if accepted {
				return true, callbackReply(cfg, func(t config.Templates) string { return t.CheckinAcceptedReply }, "本月已确认，祝君安康！")
			}
			return false, callbackReply(cfg, func(t config.Templates) string { return t.CheckinExpiredReply }, "此确认已过期，请用最新的确认消息")
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

func startHealthcheckRunner(ctx context.Context, cfg *config.Config, store *state.Store, logf func(string, ...any)) {
	if !cfg.Reliability.Healthcheck.Enabled {
		return
	}
	runner := reliability.NewHealthcheckRunner(cfg.Reliability.Healthcheck, store, logf)
	go runner.Run(ctx)
	logf("外部探活 ping 已启用，interval=%s", cfg.Reliability.Healthcheck.Interval.Std())
}

func callbackReply(cfg *config.Config, pick func(config.Templates) string, fallback string) string {
	if cfg == nil {
		return fallback
	}
	if text := pick(cfg.Lang("zh")); text != "" {
		return text
	}
	return fallback
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

func startDownloadServer(ctx context.Context, cfg *config.Config, store *state.Store, logf func(string, ...any)) {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Download.SelfHosted.ListenPort),
		Handler:           download.NewServer(store).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logf("self_hosted 下载服务启动: %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logf("self_hosted 下载服务异常退出: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logf("self_hosted 下载服务关闭失败: %v", err)
		}
	}()
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
		if err := os.MkdirAll(filepath.Dir(cfg.Logging.File), 0o700); err != nil {
			logger.Printf("警告：创建日志目录失败，日志将仅输出到 stdout: %v", err)
		} else if f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
			logger.Printf("警告：打开日志文件失败，日志将仅输出到 stdout: %v", err)
		} else {
			logger = log.New(newMultiWriter(os.Stdout, f), "", log.LstdFlags|log.LUTC)
		}
	}
	return func(format string, args ...any) {
		logger.Printf(format, args...)
	}
}
