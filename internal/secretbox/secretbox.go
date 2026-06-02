// Package secretbox 加密需要短期保存在 state.db 中的敏感字段。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	prefix   = "ifgone:v1:"
	keyLen   = 32
	saltLen  = 16
	nonceLen = 12
)

func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, prefix)
}

func Encrypt(plaintext, passphrase string) (string, error) {
	if passphrase == "" {
		return "", fmt.Errorf("passphrase 不能为空")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(append(salt, nonce...), ciphertext...)
	return prefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func Decrypt(value, passphrase string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	if passphrase == "" {
		return "", fmt.Errorf("passphrase 不能为空")
	}
	encoded := strings.TrimPrefix(value, prefix)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(payload) <= saltLen+nonceLen {
		return "", fmt.Errorf("密文长度不正确")
	}
	salt := payload[:saltLen]
	nonce := payload[saltLen : saltLen+nonceLen]
	ciphertext := payload[saltLen+nonceLen:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, keyLen)
}
