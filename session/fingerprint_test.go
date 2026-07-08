package session

import (
	"net/http/httptest"
	"testing"
)

func TestFingerprintStableAndHeaderSensitive(t *testing.T) {
	r1 := httptest.NewRequest("GET", "/", nil)
	r1.Header.Set("User-Agent", "Mozilla/5.0")
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("User-Agent", "Mozilla/5.0")
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.Header.Set("User-Agent", "curl/8")

	hdrs := []string{"User-Agent"}
	a := computeFingerprint(hdrs, r1)
	b := computeFingerprint(hdrs, r2)
	c := computeFingerprint(hdrs, r3)
	if a == "" || a != b {
		t.Fatalf("same UA must match: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different UA must differ")
	}
}

func TestFingerprintEqualConstantTime(t *testing.T) {
	if !fingerprintEqual("abc", "abc") {
		t.Fatal("equal should be true")
	}
	if fingerprintEqual("abc", "abd") || fingerprintEqual("abc", "ab") {
		t.Fatal("unequal should be false")
	}
}
