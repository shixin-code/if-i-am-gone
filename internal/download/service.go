// Package download 提供 self_hosted 与 S3-compatible 下载链接生成。
package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

// Store 是下载服务依赖的持久化能力，便于测试替换。
type Store interface {
	CreateDownloadToken(t state.DownloadToken) error
	GetDownloadToken(token string) (*state.DownloadToken, error)
	IncrementDownloadCount(token string) error
	Audit(event, detail string, at time.Time) error
}

// Service 负责生成下载 token 和公开 URL。
type Service struct {
	cfg      *config.Config
	store    Store
	s3       s3Linker
	tokenGen func() (string, error)
	now      func() time.Time
}

type s3Linker interface {
	UploadAndPresign(ctx context.Context, archivePath string, expires time.Duration) (string, time.Time, error)
}

func NewService(cfg *config.Config, store Store) *Service {
	s := &Service{
		cfg:      cfg,
		store:    store,
		tokenGen: randomToken,
		now:      func() time.Time { return time.Now().UTC() },
	}
	if cfg.Download.Mode == "s3" {
		s.s3 = newS3Client(cfg)
	}
	return s
}

func NewServiceForTest(cfg *config.Config, store Store, tokenGen func() (string, error), now func() time.Time) *Service {
	s := NewService(cfg, store)
	if tokenGen != nil {
		s.tokenGen = tokenGen
	}
	if now != nil {
		s.now = now
	}
	return s
}

func NewServiceWithS3ForTest(cfg *config.Config, store Store, s3 s3Linker, now func() time.Time) *Service {
	s := NewService(cfg, store)
	s.s3 = s3
	if now != nil {
		s.now = now
	}
	return s
}

func (s *Service) CreateLink(archivePath, beneficiaryEmail string) (link string, expiresAt time.Time, maxDownloads int, err error) {
	if archivePath == "" {
		return "", time.Time{}, 0, fmt.Errorf("archivePath 不能为空")
	}
	switch s.cfg.Download.Mode {
	case "self_hosted":
		return s.createSelfHostedLink(archivePath, beneficiaryEmail)
	case "s3":
		return s.createS3Link(archivePath, beneficiaryEmail)
	default:
		return "", time.Time{}, 0, fmt.Errorf("download.mode=%s 不支持", s.cfg.Download.Mode)
	}
}

func (s *Service) createSelfHostedLink(archivePath, beneficiaryEmail string) (link string, expiresAt time.Time, maxDownloads int, err error) {
	if strings.TrimSpace(s.cfg.Download.SelfHosted.PublicBaseURL) == "" {
		return "", time.Time{}, 0, fmt.Errorf("download.self_hosted.public_base_url 不能为空")
	}

	token, err := s.tokenGen()
	if err != nil {
		return "", time.Time{}, 0, err
	}
	link, err = s.publicURL(token)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	expiresAt = s.now().Add(s.cfg.Download.LinkExpiry.Std()).UTC()
	maxDownloads = s.cfg.Download.MaxDownloads
	if maxDownloads <= 0 {
		maxDownloads = 1
	}
	if err := s.store.CreateDownloadToken(state.DownloadToken{
		Token:        token,
		ArchivePath:  archivePath,
		Beneficiary:  beneficiaryEmail,
		ExpiresAt:    expiresAt,
		MaxDownloads: maxDownloads,
	}); err != nil {
		return "", time.Time{}, 0, err
	}
	_ = s.store.Audit("download_token_created", beneficiaryEmail, s.now())
	return link, expiresAt, maxDownloads, nil
}

func (s *Service) createS3Link(archivePath, beneficiaryEmail string) (link string, expiresAt time.Time, maxDownloads int, err error) {
	if s.s3 == nil {
		return "", time.Time{}, 0, fmt.Errorf("S3 下载链接服务未初始化")
	}
	expiry := s.cfg.Download.S3.PresignExpiry.Std()
	if expiry <= 0 {
		expiry = s.cfg.Download.LinkExpiry.Std()
	}
	link, expiresAt, err = s.s3.UploadAndPresign(context.Background(), archivePath, expiry)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	_ = s.store.Audit("s3_presigned_link_created", fmt.Sprintf("%s:%s", beneficiaryEmail, filepath.Base(archivePath)), s.now())
	return link, expiresAt.UTC(), 0, nil
}

func (s *Service) publicURL(token string) (string, error) {
	base, err := url.Parse(s.cfg.Download.SelfHosted.PublicBaseURL)
	if err != nil {
		return "", fmt.Errorf("解析 public_base_url 失败: %w", err)
	}
	base.Path = path.Join(base.Path, "/download", token)
	return base.String(), nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
