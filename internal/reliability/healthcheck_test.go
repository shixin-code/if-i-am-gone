package reliability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
)

type memoryAuditStore struct {
	events []string
}

func (m *memoryAuditStore) Audit(event, detail string, at time.Time) error {
	m.events = append(m.events, event+":"+detail)
	return nil
}

func TestHealthcheckPingOnceRecordsSuccess(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method=%s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	store := &memoryAuditStore{}
	runner := NewHealthcheckRunnerForTest(config.HealthcheckConfig{
		Enabled: true,
		PingURL: server.URL,
		Timeout: config.Duration(time.Second),
	}, store, server.Client(), func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }, nil)

	if err := runner.PingOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("请求次数=%d", requests)
	}
	if len(store.events) != 1 || !strings.HasPrefix(store.events[0], "healthcheck_ping_ok:") {
		t.Fatalf("审计不对: %+v", store.events)
	}
}

func TestHealthcheckPingOnceRecordsFailureWithoutPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	store := &memoryAuditStore{}
	runner := NewHealthcheckRunnerForTest(config.HealthcheckConfig{
		Enabled: true,
		PingURL: server.URL,
		Timeout: config.Duration(time.Second),
	}, store, server.Client(), func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }, nil)

	err := runner.PingOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status=503") {
		t.Fatalf("期望 503 错误，实际 %v", err)
	}
	if len(store.events) != 1 || !strings.HasPrefix(store.events[0], "healthcheck_ping_failed:") {
		t.Fatalf("审计不对: %+v", store.events)
	}
}
