package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"testing"
)

func TestDataRoundtripWithoutHeaders(t *testing.T) {
	key := "12345678901234567890123456789012"
	data := Data{JWT: "j", SessionID: "s"}
	enc, err := EncryptSessionCookie(data, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := DecryptSessionCookie(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if out.Headers != nil {
		t.Errorf("expected nil Headers, got %v", out.Headers)
	}
}

func TestDataRoundtripWithHeaders(t *testing.T) {
	key := "12345678901234567890123456789012"
	data := Data{
		JWT: "j", SessionID: "s",
		Headers: map[string]string{"X-User-Id": "abc", "X-User-Roles": "A,B"},
	}
	enc, err := EncryptSessionCookie(data, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := DecryptSessionCookie(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if out.Headers["X-User-Id"] != "abc" || out.Headers["X-User-Roles"] != "A,B" {
		t.Errorf("headers roundtrip mismatch: %v", out.Headers)
	}
}

func TestDataRoundtripWithSubEmailName(t *testing.T) {
	key := "12345678901234567890123456789012"
	data := Data{
		JWT:       "j",
		SessionID: "s",
		Sub:       "user-123",
		Email:     "user@example.com",
		Name:      "Test User",
	}
	enc, err := EncryptSessionCookie(data, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := DecryptSessionCookie(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if out.Sub != "user-123" {
		t.Errorf("Sub: got %q, want %q", out.Sub, "user-123")
	}
	if out.Email != "user@example.com" {
		t.Errorf("Email: got %q, want %q", out.Email, "user@example.com")
	}
	if out.Name != "Test User" {
		t.Errorf("Name: got %q, want %q", out.Name, "Test User")
	}
}

func TestDataDecryptOldFormatWithIdentity(t *testing.T) {
	// Simulate a cookie produced by the previous plugin version: contains
	// the legacy "identity" JSON key but no sub/email/name. After this
	// release, the Data struct no longer has Identity; encoding/json should
	// silently drop the unknown key on decode and produce empty Sub/Email/Name.
	key := "12345678901234567890123456789012"

	oldPlaintext := []byte(`{"jwt":"j","identity":"i","session_id":"s"}`)

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, oldPlaintext, nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	out, err := DecryptSessionCookie(encoded, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if out.JWT != "j" {
		t.Errorf("JWT: got %q, want %q", out.JWT, "j")
	}
	if out.SessionID != "s" {
		t.Errorf("SessionID: got %q, want %q", out.SessionID, "s")
	}
	if out.Sub != "" {
		t.Errorf("Sub: got %q, want empty (no sub in old format)", out.Sub)
	}
	if out.Email != "" {
		t.Errorf("Email: got %q, want empty", out.Email)
	}
	if out.Name != "" {
		t.Errorf("Name: got %q, want empty", out.Name)
	}
}
