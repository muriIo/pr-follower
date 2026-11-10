package crypto_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/crypto"
)

func testKey(fill byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(fill), 32)))
}

func TestAESGCMCipher_RoundTripAndNonceUniqueness(t *testing.T) {
	t.Parallel()
	c, err := crypto.NewAESGCMCipher(testKey('k'))
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error: %v", err)
	}

	a, err := c.Encrypt("ghu_secret")
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	b, _ := c.Encrypt("ghu_secret")
	if a == b {
		t.Fatal("expected a fresh nonce per encryption")
	}
	if strings.Contains(a, "ghu_secret") {
		t.Fatal("ciphertext leaks plaintext")
	}

	got, err := c.Decrypt(a)
	if err != nil || got != "ghu_secret" {
		t.Fatalf("Decrypt() = %q, %v", got, err)
	}
}

func TestAESGCMCipher_EmptyStaysEmpty(t *testing.T) {
	t.Parallel()
	c, _ := crypto.NewAESGCMCipher(testKey('k'))
	enc, err := c.Encrypt("")
	if err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v", enc, err)
	}
	dec, err := c.Decrypt("")
	if err != nil || dec != "" {
		t.Fatalf("Decrypt(\"\") = %q, %v", dec, err)
	}
}

func TestAESGCMCipher_RejectsTamperingAndWrongKey(t *testing.T) {
	t.Parallel()
	c, _ := crypto.NewAESGCMCipher(testKey('k'))
	other, _ := crypto.NewAESGCMCipher(testKey('o'))
	enc, _ := c.Encrypt("ghu_secret")

	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("expected wrong key to fail")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xFF
	if _, err := c.Decrypt(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("expected tampered ciphertext to fail")
	}
	if _, err := c.Decrypt("not base64 !!"); err == nil {
		t.Fatal("expected malformed ciphertext to fail")
	}
}

func TestNewAESGCMCipher_RejectsBadKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", "not-base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := crypto.NewAESGCMCipher(key); err == nil {
			t.Errorf("expected key %q to be rejected", key)
		}
	}
}

func TestGenerateKey_ProducesUsableDistinctKeys(t *testing.T) {
	t.Parallel()
	a, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error: %v", err)
	}
	b, _ := crypto.GenerateKey()
	if a == b {
		t.Fatal("expected distinct random keys")
	}
	if _, err := crypto.NewAESGCMCipher(a); err != nil {
		t.Fatalf("generated key rejected: %v", err)
	}
}
