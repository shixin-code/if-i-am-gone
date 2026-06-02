// Package reliability 提供不影响核心投递流程的可靠性辅助能力。
package reliability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
)

// Store 是探活模块需要的最小审计能力。
type Store interface {
	Audit(event, detail string, at time.Time) error
}

// Runner 定时访问第三方 healthcheck ping URL。
type Runner struct {
	cfg    config.HealthcheckConfig
	store  Store
	client *http.Client
	now    func() time.Time
	logf   func(string, ...any)
}

func NewHealthcheckRunner(cfg config.HealthcheckConfig, store Store, logf func(string, ...any)) *Runner {
	timeout := cfg.Timeout.Std()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Runner{
		cfg:    cfg,
		store:  store,
		client: &http.Client{Timeout: timeout},
		now:    func() time.Time { return time.Now().UTC() },
		logf:   logf,
	}
}

func NewHealthcheckRunnerForTest(cfg config.HealthcheckConfig, store Store, client *http.Client, now func() time.Time, logf func(string, ...any)) *Runner {
	r := NewHealthcheckRunner(cfg, store, logf)
	if client != nil {
		r.client = client
	}
	if now != nil {
		r.now = now
	}
	return r
}

func (r *Runner) Run(ctx context.Context) {
	if r == nil || !r.cfg.Enabled {
		return
	}
	interval := r.cfg.Interval.Std()
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	r.ping(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ping(ctx)
		}
	}
}

func (r *Runner) PingOnce(ctx context.Context) error {
	return r.ping(ctx)
}

func (r *Runner) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.PingURL, nil)
	if err != nil {
		return r.recordFailure("构造探活请求失败", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return r.recordFailure("探活 ping 失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return r.recordFailure("探活 ping 返回非 2xx", fmt.Errorf("status=%d", resp.StatusCode))
	}
	_ = r.store.Audit("healthcheck_ping_ok", r.cfg.PingURL, r.now())
	r.logf("外部探活 ping 成功")
	return nil
}

func (r *Runner) recordFailure(prefix string, err error) error {
	detail := fmt.Sprintf("%s: %v", prefix, err)
	_ = r.store.Audit("healthcheck_ping_failed", detail, r.now())
	r.logf("%s", detail)
	return fmt.Errorf("%s", detail)
}
