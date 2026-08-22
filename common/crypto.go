package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

func GenerateHMACWithKey(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func Password2Hash(password string) (string, error) {
	passwordBytes := []byte(password)
	hashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, bcrypt.DefaultCost)
	return string(hashedPassword), err
}

func ValidatePasswordAndHash(password string, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

const encryptedTextPrefix = "enc:v1:"

var (
	assetCredentialKeyOnce sync.Once
	assetCredentialKey     []byte
	assetCredentialKeyErr  error
)

// AssetCredentialKey returns the 32-byte key used to encrypt asset library
// channel credentials at rest. Resolution order:
//  1. ASSET_CREDENTIAL_SECRET environment variable
//  2. CRYPTO_SECRET / SESSION_SECRET environment variable (via CryptoSecret)
//  3. a random key persisted to ./asset_credential.key so ciphertext stays
//     decryptable across restarts even without any secret configured
func AssetCredentialKey() ([]byte, error) {
	assetCredentialKeyOnce.Do(func() {
		if secret := os.Getenv("ASSET_CREDENTIAL_SECRET"); secret != "" {
			sum := sha256.Sum256([]byte(secret))
			assetCredentialKey = sum[:]
			return
		}
		if os.Getenv("CRYPTO_SECRET") != "" || os.Getenv("SESSION_SECRET") != "" {
			sum := sha256.Sum256([]byte(CryptoSecret))
			assetCredentialKey = sum[:]
			return
		}
		const keyPath = "asset_credential.key"
		if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
			assetCredentialKey = data
			return
		}
		buf := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			assetCredentialKeyErr = err
			return
		}
		if err := os.WriteFile(keyPath, buf, 0600); err != nil {
			assetCredentialKeyErr = err
			return
		}
		assetCredentialKey = buf
	})
	return assetCredentialKey, assetCredentialKeyErr
}

// EncryptText encrypts plaintext with AES-256-GCM and returns a prefixed,
// base64-encoded ciphertext. Empty input stays empty.
func EncryptText(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := AssetCredentialKey()
	if err != nil {
		return "", err
	}
	gcm, err := newAESGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedTextPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptText reverses EncryptText. Values without the encryption prefix are
// returned unchanged so legacy plaintext rows keep working; they are migrated
// to ciphertext the next time they are saved.
func DecryptText(stored string) (string, error) {
	if !strings.HasPrefix(stored, encryptedTextPrefix) {
		return stored, nil
	}
	key, err := AssetCredentialKey()
	if err != nil {
		return "", err
	}
	gcm, err := newAESGCM(key)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encryptedTextPrefix))
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("encrypted value is too short")
	}
	plaintext, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("failed to decrypt encrypted value (key mismatch?)")
	}
	return string(plaintext), nil
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// IsEncryptedText reports whether a stored value is encrypted ciphertext.
func IsEncryptedText(stored string) bool {
	return strings.HasPrefix(stored, encryptedTextPrefix)
}
