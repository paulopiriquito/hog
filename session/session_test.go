package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPublicViewExcludesSecretsAndComputesExpiresIn(t *testing.T) {
	now := time.Now()
	s := &Session{
		Subject:     "u-1",
		Passport:    map[string]any{"email": "a@b.co"},
		Groups:      []string{"admins"},
		AccessToken: "at-secret",
		Expiry:      now.Add(2 * time.Hour),
		IssuedAt:    now,
		Fingerprint: "fp-secret",
	}
	pv := s.PublicView()
	if pv.Subject != "u-1" || pv.Passport["email"] != "a@b.co" || len(pv.Groups) != 1 {
		t.Fatalf("public view = %+v", pv)
	}
	if pv.ExpiresIn <= 0 || pv.ExpiresIn > 7200 {
		t.Fatalf("expiresIn = %d, want ~7200", pv.ExpiresIn)
	}
	b := mustJSON(t, pv)
	for _, leak := range []string{"at-secret", "fp-secret", "AccessToken", "Fingerprint"} {
		if containsStr(b, leak) {
			t.Fatalf("public view leaks %q: %s", leak, b)
		}
	}
}

func TestExpired(t *testing.T) {
	if !(&Session{Expiry: time.Now().Add(-time.Minute)}).Expired() {
		t.Fatal("past expiry should be expired")
	}
	if (&Session{Expiry: time.Now().Add(time.Minute)}).Expired() {
		t.Fatal("future expiry should not be expired")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func containsStr(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
