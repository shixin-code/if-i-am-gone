package packer

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeka/zip"
)

var testTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// 打包后用 yeka/zip 自己读回，验证密码正确、内容一致。
func TestBuildAndReadBack(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	want := []byte("绝密遗嘱内容 secret will\n")
	if err := os.WriteFile(filepath.Join(src, "will.txt"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	// 子目录也要进包。
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Build(src, dst, 24, testTime)
	if err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	if res.Password == "" || len(res.Password) != 24 {
		t.Fatalf("密码长度不对: %q", res.Password)
	}
	if res.SHA256 == "" {
		t.Fatal("缺少 sha256")
	}

	// 用正确密码读回 will.txt。
	r, err := zip.OpenReader(res.Path)
	if err != nil {
		t.Fatalf("打开 zip 失败: %v", err)
	}
	defer r.Close()
	found := false
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Errorf("条目 %s 未加密", f.Name)
		}
		f.SetPassword(res.Password)
		if f.Name == "will.txt" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("解密 will.txt 失败: %v", err)
			}
			got, _ := io.ReadAll(rc)
			rc.Close()
			if string(got) != string(want) {
				t.Errorf("内容不符: got %q want %q", got, want)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("zip 内未找到 will.txt")
	}
}

// 错误密码应解密失败。
func TestWrongPasswordFails(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("data"), 0o600)

	res, err := Build(src, dst, 16, testTime)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := zip.OpenReader(res.Path)
	defer r.Close()
	for _, f := range r.File {
		f.SetPassword("wrong-password")
		rc, err := f.Open()
		if err != nil {
			continue // 打开即失败也算「拒绝」
		}
		if _, err := io.ReadAll(rc); err == nil {
			t.Error("错误密码不应能读出内容")
		}
		rc.Close()
	}
}

// 若环境装了 7z/7za，验证产物能被 7-Zip 解压（真实兼容性）。
func TestCompatibleWith7z(t *testing.T) {
	bin := find7z()
	if bin == "" {
		t.Skip("未找到 7z/7za，跳过 7-Zip 兼容性测试")
	}
	src := t.TempDir()
	dst := t.TempDir()
	want := []byte("7zip-compat-check")
	os.WriteFile(filepath.Join(src, "data.txt"), want, 0o600)

	res, err := Build(src, dst, 20, testTime)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	// 7z x -p<password> -o<out> <zip> -y
	cmd := exec.Command(bin, "x", "-p"+res.Password, "-o"+out, res.Path, "-y")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7z 解压失败: %v\n%s", err, combined)
	}
	got, err := os.ReadFile(filepath.Join(out, "data.txt"))
	if err != nil {
		t.Fatalf("读取解压结果失败: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("7z 解压内容不符: got %q want %q", got, want)
	}
}

func find7z() string {
	for _, name := range []string{"7z", "7za", "7zz"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func TestCleanupOld(t *testing.T) {
	dst := t.TempDir()
	// 造 5 个不同时间戳的包。
	for i := 0; i < 5; i++ {
		name := filepath.Join(dst, "archive-2026010"+string(rune('1'+i))+"-000000.zip")
		os.WriteFile(name, []byte("x"), 0o600)
	}
	if err := CleanupOld(dst, 3); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dst, "archive-*.zip"))
	if len(matches) != 3 {
		t.Fatalf("应保留 3 个，实际 %d", len(matches))
	}
}

func TestGenPasswordUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		p, err := GenPassword(32)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 32 {
			t.Fatalf("长度不对: %d", len(p))
		}
		if seen[p] {
			t.Fatal("生成了重复密码")
		}
		seen[p] = true
	}
}
