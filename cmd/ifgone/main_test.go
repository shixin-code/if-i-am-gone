package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ofilm/if-i-am-gone/internal/config"
)

func TestSetupLoggerWarnsWhenLogFileCannotBeOpened(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	dir := t.TempDir()
	cfg := &config.Config{Logging: config.Logging{File: filepath.Join(dir, "as-dir")}}
	if err := os.Mkdir(cfg.Logging.File, 0o700); err != nil {
		t.Fatal(err)
	}
	logf := setupLogger(cfg)
	logf("after warning")

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "警告：打开日志文件失败") {
		t.Fatalf("未看到日志文件失败告警: %s", text)
	}
	if !strings.Contains(text, "after warning") {
		t.Fatalf("回退 stdout 后应继续输出日志: %s", text)
	}
}

func TestCallbackReplyUsesConfiguredTemplate(t *testing.T) {
	cfg := &config.Config{Templates: map[string]config.Templates{
		"zh": {CheckinAcceptedReply: "配置确认成功"},
	}}
	got := callbackReply(cfg, func(t config.Templates) string { return t.CheckinAcceptedReply }, "默认确认成功")
	if got != "配置确认成功" {
		t.Fatalf("应使用配置文案，实际 %q", got)
	}
}

func TestCallbackReplyFallsBackWhenTemplateMissing(t *testing.T) {
	cfg := &config.Config{Templates: map[string]config.Templates{"zh": {}}}
	got := callbackReply(cfg, func(t config.Templates) string { return t.CheckinExpiredReply }, "默认过期")
	if got != "默认过期" {
		t.Fatalf("应回退默认文案，实际 %q", got)
	}
}
