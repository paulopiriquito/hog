package session

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func decodeNode(t *testing.T, s string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return *n.Content[0]
	}
	return n
}

const key32 = "0123456789abcdef0123456789abcdef" // 32 bytes

func TestFromYAMLDefaults(t *testing.T) {
	cfg, err := FromYAML(decodeNode(t, "key: "+key32+"\n"))
	if err != nil {
		t.Fatalf("FromYAML: %v", err)
	}
	if cfg.CookieName != "hog_session" {
		t.Fatalf("cookieName = %q", cfg.CookieName)
	}
	if cfg.TTL != 8*time.Hour {
		t.Fatalf("ttl default = %v, want 8h", cfg.TTL)
	}
	if len(cfg.Key) != 32 {
		t.Fatalf("key length = %d, want 32", len(cfg.Key))
	}
	if len(cfg.FingerprintHeaders) != 1 || cfg.FingerprintHeaders[0] != "User-Agent" {
		t.Fatalf("fingerprintHeaders default = %v", cfg.FingerprintHeaders)
	}
	if cfg.InfoPath != "/auth/session" || cfg.PostLogoutRedirect != "/" {
		t.Fatalf("info/postLogout defaults = %q %q", cfg.InfoPath, cfg.PostLogoutRedirect)
	}
}

func TestFromYAMLKeyMustBe32(t *testing.T) {
	if _, err := FromYAML(decodeNode(t, "key: short\n")); err == nil {
		t.Fatal("want error for non-32-byte key")
	}
	if _, err := FromYAML(decodeNode(t, "cookieName: x\n")); err == nil {
		t.Fatal("want error for missing key")
	}
}
