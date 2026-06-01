// Package packer 把源目录打包为 AES-256 加密 ZIP（WinZip AES 标准），
// 产物可被 7-Zip / WinRAR 解压。每次打包生成新的强随机密码。
package packer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yeka/zip"
)

// passwordAlphabet 排除了容易混淆的字符（0/O、1/l/I），便于人工转录密码。
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*-_=+"

// GenPassword 生成长度为 n 的密码学强随机密码。
func GenPassword(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	var sb strings.Builder
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("生成随机密码失败: %w", err)
		}
		sb.WriteByte(passwordAlphabet[idx.Int64()])
	}
	return sb.String(), nil
}

// Result 是一次打包的结果。
type Result struct {
	Path     string
	SHA256   string
	Size     int64
	Password string
}

// Build 把 sourceDir 下所有文件递归打包进一个 AES-256 加密 ZIP，
// 写入 destDir，文件名带时间戳。返回路径、sha256、大小与所用密码。
//
// now 由调用方注入，便于测试与避免 Date.now 类不确定性。
func Build(sourceDir, destDir string, passwordLength int, now time.Time) (*Result, error) {
	info, err := os.Stat(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("源目录不可访问 %s: %w", sourceDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source_dir %s 不是目录", sourceDir)
	}

	password, err := GenPassword(passwordLength)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建目标目录失败: %w", err)
	}
	outPath := filepath.Join(destDir, fmt.Sprintf("archive-%s.zip", now.UTC().Format("20060102-150405")))

	fout, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("创建 zip 文件失败: %w", err)
	}
	// 出错时清理半成品。
	cleanupOnErr := true
	defer func() {
		fout.Close()
		if cleanupOnErr {
			os.Remove(outPath)
		}
	}()

	zw := zip.NewWriter(fout)
	walkErr := filepath.Walk(sourceDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		// ZIP 内统一用正斜杠。
		rel = filepath.ToSlash(rel)

		w, err := zw.Encrypt(rel, password, zip.AES256Encryption)
		if err != nil {
			return fmt.Errorf("加密条目 %s 失败: %w", rel, err)
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.Copy(w, src); err != nil {
			return fmt.Errorf("写入条目 %s 失败: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		zw.Close()
		return nil, fmt.Errorf("打包失败: %w", walkErr)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 zip 失败: %w", err)
	}
	if err := fout.Sync(); err != nil {
		return nil, fmt.Errorf("刷盘失败: %w", err)
	}

	sum, size, err := hashFile(outPath)
	if err != nil {
		return nil, err
	}

	cleanupOnErr = false
	return &Result{Path: outPath, SHA256: sum, Size: size, Password: password}, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// CleanupOld 只保留 destDir 下最新的 keep 个 archive-*.zip，删除其余。
func CleanupOld(destDir string, keep int) error {
	if keep <= 0 {
		keep = 1
	}
	matches, err := filepath.Glob(filepath.Join(destDir, "archive-*.zip"))
	if err != nil {
		return err
	}
	if len(matches) <= keep {
		return nil
	}
	// 文件名含时间戳，字典序即时间序；降序后保留前 keep 个。
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for _, p := range matches[keep:] {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}
