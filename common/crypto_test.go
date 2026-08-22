package common

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestEncryptDecryptTextRoundTrip(t *testing.T) {
	resetAssetCredentialKey(t, "test-asset-credential-secret")
	plaintext := "sk-very-secret-key-123"
	encrypted, err := EncryptText(plaintext)
	if err != nil {
		t.Fatalf("EncryptText failed: %v", err)
	}
	if encrypted == plaintext {
		t.Fatal("ciphertext must differ from plaintext")
	}
	if !IsEncryptedText(encrypted) {
		t.Fatal("ciphertext must carry the encryption prefix")
	}
	if strings.Contains(encrypted, plaintext) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	decrypted, err := DecryptText(encrypted)
	if err != nil {
		t.Fatalf("DecryptText failed: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("round trip mismatch: got %q", decrypted)
	}
}

func TestEncryptTextEmptyStaysEmpty(t *testing.T) {
	resetAssetCredentialKey(t, "test-asset-credential-secret")
	encrypted, err := EncryptText("")
	if err != nil || encrypted != "" {
		t.Fatalf("empty input must stay empty, got %q err=%v", encrypted, err)
	}
}

func TestDecryptTextLegacyPlaintextPassesThrough(t *testing.T) {
	resetAssetCredentialKey(t, "test-asset-credential-secret")
	decrypted, err := DecryptText("legacy-plaintext-secret")
	if err != nil {
		t.Fatalf("legacy plaintext must pass through: %v", err)
	}
	if decrypted != "legacy-plaintext-secret" {
		t.Fatalf("legacy plaintext must be unchanged, got %q", decrypted)
	}
}

func TestDecryptTextRejectsTamperedCiphertext(t *testing.T) {
	resetAssetCredentialKey(t, "test-asset-credential-secret")
	encrypted, err := EncryptText("secret-value")
	if err != nil {
		t.Fatalf("EncryptText failed: %v", err)
	}
	tampered := encrypted[:len(encrypted)-2] + "AA"
	if _, err := DecryptText(tampered); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestAssetCredentialKeyFileFallback(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	resetAssetCredentialKey(t, "")

	key, err := AssetCredentialKey()
	if err != nil {
		t.Fatalf("AssetCredentialKey failed: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key must be 32 bytes, got %d", len(key))
	}
	if _, err := os.Stat(filepath.Join(dir, "asset_credential.key")); err != nil {
		t.Fatalf("key file must be persisted: %v", err)
	}

	// A second resolution must load the same key from the file.
	resetAssetCredentialKey(t, "")
	key2, err := AssetCredentialKey()
	if err != nil {
		t.Fatalf("second AssetCredentialKey failed: %v", err)
	}
	if string(key) != string(key2) {
		t.Fatal("key file fallback must be stable across resolutions")
	}
}

// resetAssetCredentialKey re-arms the once initializer with a controlled
// environment for deterministic tests.
func resetAssetCredentialKey(t *testing.T, secret string) {
	t.Helper()
	t.Setenv("ASSET_CREDENTIAL_SECRET", secret)
	t.Setenv("CRYPTO_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	assetCredentialKeyOnce = sync.Once{}
	assetCredentialKey = nil
	assetCredentialKeyErr = nil
}
