package download

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/ofilm/if-i-am-gone/internal/config"
)

type s3Client struct {
	cfg       config.S3Config
	client    *s3.Client
	presigner *s3.PresignClient
	now       func() time.Time
	keyGen    func(string) (string, error)
}

func newS3Client(cfg *config.Config) *s3Client {
	awsCfg := aws.Config{
		Region:      cfg.Download.S3.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.Download.S3.AccessKey, cfg.Download.S3.SecretKey, ""),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Download.S3.Endpoint)
		o.UsePathStyle = true
	})
	return &s3Client{
		cfg:       cfg.Download.S3,
		client:    client,
		presigner: s3.NewPresignClient(client),
		now:       func() time.Time { return time.Now().UTC() },
		keyGen:    s3ObjectKey,
	}
}

func (c *s3Client) UploadAndPresign(ctx context.Context, archivePath string, expires time.Duration) (string, time.Time, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("打开待上传归档失败: %w", err)
	}
	defer file.Close()

	key, err := c.keyGen(archivePath)
	if err != nil {
		return "", time.Time{}, err
	}
	contentType := mime.TypeByExtension(filepath.Ext(archivePath))
	if contentType == "" {
		contentType = "application/zip"
	}
	_, err = c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.cfg.Bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(contentType),
		ACL:         types.ObjectCannedACLPrivate,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("上传归档到 S3 失败: %w", err)
	}
	presigned, err := c.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.cfg.Bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expires
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("生成 S3 预签名链接失败: %w", err)
	}
	return presigned.URL, c.now().Add(expires).UTC(), nil
}

func s3ObjectKey(archivePath string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	name := filepath.Base(archivePath)
	name = strings.TrimSpace(strings.ReplaceAll(name, string(filepath.Separator), "_"))
	if name == "" || name == "." {
		name = "archive.zip"
	}
	return fmt.Sprintf("ifgone/%s/%s-%s", time.Now().UTC().Format("2006-01-02"), token[:16], name), nil
}
