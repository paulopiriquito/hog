package session

import "testing"

func TestDataRoundtripWithoutHeaders(t *testing.T) {
	key := "12345678901234567890123456789012"
	data := Data{JWT: "j", Identity: "i", SessionID: "s"}
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
		JWT: "j", Identity: "i", SessionID: "s",
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
