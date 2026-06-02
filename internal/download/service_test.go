package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

type memoryStore struct {
	tokens map[string]state.DownloadToken
	audits []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{tokens: map[string]state.DownloadToken{}}
}

func (m *memoryStore) CreateDownloadToken(t state.DownloadToken) error {
	t.DownloadCount = 0
	m.tokens[t.Token] = t
	return nil
}

func (m *memoryStore) GetDownloadToken(token string) (*state.DownloadToken, error) {
	t, ok := m.tokens[token]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (m *memoryStore) IncrementDownloadCount(token string) error {
	t := m.tokens[token]
	t.DownloadCount++
	m.tokens[token] = t
	return nil
}

func (m *memoryStore) Audit(event, detail string, at time.Time) error {
	m.audits = append(m.audits, event+":"+detail)
	return nil
}

func testConfig() *config.Config {
	return &config.Config{
		Download: config.Download{
			Mode:         "self_hosted",
			LinkExpiry:   config.Duration(24 * time.Hour),
			MaxDownloads: 2,
			SelfHosted:   config.SelfHostedConfig{PublicBaseURL: "https://example.com/base"},
		},
	}
}

type fakeS3Linker struct {
	archivePath string
	expires     time.Duration
	url         string
	expiresAt   time.Time
}

func (f *fakeS3Linker) UploadAndPresign(ctx context.Context, archivePath string, expires time.Duration) (string, time.Time, error) {
	f.archivePath = archivePath
	f.expires = expires
	return f.url, f.expiresAt, nil
}

func TestCreateLinkStoresTokenAndBuildsURL(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := NewServiceForTest(testConfig(), store, func() (string, error) { return "tok123", nil }, func() time.Time { return now })

	link, expiresAt, maxDownloads, err := svc.CreateLink("/tmp/archive.zip", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://example.com/base/download/tok123" {
		t.Fatalf("链接不对: %s", link)
	}
	if !expiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("过期时间不对: %s", expiresAt)
	}
	if maxDownloads != 2 {
		t.Fatalf("下载次数不对: %d", maxDownloads)
	}
	tok := store.tokens["tok123"]
	if tok.ArchivePath != "/tmp/archive.zip" || tok.Beneficiary != "a@example.com" {
		t.Fatalf("token 记录不对: %+v", tok)
	}
}

func TestCreateS3LinkUploadsAndPresignsWithoutLocalToken(t *testing.T) {
	cfg := testConfig()
	cfg.Download.Mode = "s3"
	cfg.Download.S3 = config.S3Config{
		Endpoint:      "https://s3.example.com",
		Bucket:        "ifgone",
		Region:        "us-east-1",
		AccessKey:     "key",
		SecretKey:     "secret",
		PresignExpiry: config.Duration(6 * time.Hour),
	}
	store := newMemoryStore()
	expiresAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	fake := &fakeS3Linker{url: "https://s3.example.com/ifgone/archive.zip?sig=ok", expiresAt: expiresAt}
	svc := NewServiceWithS3ForTest(cfg, store, fake, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})

	link, gotExpiresAt, maxDownloads, err := svc.CreateLink("/tmp/archive.zip", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if link != fake.url {
		t.Fatalf("链接不对: %s", link)
	}
	if !gotExpiresAt.Equal(expiresAt) {
		t.Fatalf("过期时间不对: %s", gotExpiresAt)
	}
	if maxDownloads != 0 {
		t.Fatalf("S3 预签名链接不应返回本地下载次数限制，实际 %d", maxDownloads)
	}
	if fake.archivePath != "/tmp/archive.zip" || fake.expires != 6*time.Hour {
		t.Fatalf("S3 调用参数不对: path=%q expires=%s", fake.archivePath, fake.expires)
	}
	if len(store.tokens) != 0 {
		t.Fatalf("S3 模式不应创建 self_hosted token: %+v", store.tokens)
	}
	if len(store.audits) != 1 || store.audits[0] != "s3_presigned_link_created:a@example.com:archive.zip" {
		t.Fatalf("审计不对: %+v", store.audits)
	}
}

func TestDownloadServerServesFileAndIncrementsCount(t *testing.T) {
	store := newMemoryStore()
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(archive, []byte("zip-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.tokens["tok"] = state.DownloadToken{
		Token:        "tok",
		ArchivePath:  archive,
		Beneficiary:  "a@example.com",
		ExpiresAt:    time.Now().Add(time.Hour),
		MaxDownloads: 2,
	}
	srv := NewServerForTest(store, func() time.Time { return time.Now() })

	req := httptest.NewRequest(http.MethodGet, "/download/tok", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "zip-content" {
		t.Fatalf("body=%q", rec.Body.String())
	}
	if store.tokens["tok"].DownloadCount != 1 {
		t.Fatalf("下载次数应 +1，实际 %d", store.tokens["tok"].DownloadCount)
	}
}

func TestDownloadServerRejectsExpiredAndLimitExceeded(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	store.tokens["expired"] = state.DownloadToken{
		Token:        "expired",
		ArchivePath:  "/tmp/archive.zip",
		Beneficiary:  "a@example.com",
		ExpiresAt:    now.Add(-time.Minute),
		MaxDownloads: 2,
	}
	store.tokens["limited"] = state.DownloadToken{
		Token:         "limited",
		ArchivePath:   "/tmp/archive.zip",
		Beneficiary:   "a@example.com",
		ExpiresAt:     now.Add(time.Hour),
		DownloadCount: 2,
		MaxDownloads:  2,
	}
	srv := NewServerForTest(store, func() time.Time { return now })

	for _, token := range []string{"expired", "limited"} {
		req := httptest.NewRequest(http.MethodGet, "/download/"+token, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusGone {
			t.Fatalf("%s status=%d", token, rec.Code)
		}
	}
}
