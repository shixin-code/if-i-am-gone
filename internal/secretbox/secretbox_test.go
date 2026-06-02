package secretbox

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	encrypted, err := Encrypt("secret-password", "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "secret-password" {
		t.Fatal("密文不应等于明文")
	}
	if !IsEncrypted(encrypted) {
		t.Fatal("应带加密前缀")
	}
	plain, err := Decrypt(encrypted, "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "secret-password" {
		t.Fatalf("got %q", plain)
	}
}

func TestDecryptPlaintextCompat(t *testing.T) {
	plain, err := Decrypt("legacy-password", "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "legacy-password" {
		t.Fatalf("got %q", plain)
	}
}

func TestDecryptWrongPassphraseFails(t *testing.T) {
	encrypted, err := Encrypt("secret-password", "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(encrypted, "wrong-passphrase"); err == nil {
		t.Fatal("错误 passphrase 应解密失败")
	}
}
