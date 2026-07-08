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
		t.Errorf("cookieName = %q", cfg.CookieName)
	}
	if cfg.TTL != 8*time.Hour {
		t.Errorf("ttl = %v", cfg.TTL)
	}
	if len(cfg.FingerprintHeaders) != 1 || cfg.FingerprintHeaders[0] != "User-Agent" {
		t.Errorf("fingerprintHeaders = %v", cfg.FingerprintHeaders)
	}
	if cfg.InfoPath != "/auth/session" || cfg.PostLogoutRedirect != "/" {
		t.Errorf("infoPath=%q postLogout=%q", cfg.InfoPath, cfg.PostLogoutRedirect)
	}
	want := []string{"email", "name", "given_name", "family_name"}
	if len(cfg.PassportClaims) != len(want) {
		t.Fatalf("default claims = %v", cfg.PassportClaims)
	}
	for i := range want {
		if cfg.PassportClaims[i] != want[i] {
			t.Fatalf("default claims = %v", cfg.PassportClaims)
		}
	}
	if cfg.Groups != nil {
		t.Errorf("groups should be nil when unconfigured")
	}
	if len(cfg.Key) != 32 {
		t.Errorf("key len = %d", len(cfg.Key))
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

func TestFromYAMLExplicitEmptyClaimsMeansSubOnly(t *testing.T) {
	cfg, err := FromYAML(decodeNode(t, "key: "+key32+"\npassport:\n  claims: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.PassportClaims) != 0 {
		t.Fatalf("explicit [] should yield no extra claims, got %v", cfg.PassportClaims)
	}
}

func TestFromYAMLGroupsAndOverrides(t *testing.T) {
	cfg, err := FromYAML(decodeNode(t, `key: `+key32+`
ttl: 2h
cookieName: sess
fingerprintHeaders: [User-Agent, Accept-Language]
passport:
  claims: [email, departmentnumber]
groups:
  source: isMemberOf
  match: ["ou=applicationRole"]
  render: dn
  as: roles
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CookieName != "sess" || cfg.TTL != 2*time.Hour {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.Groups == nil || cfg.Groups.Source != "isMemberOf" || cfg.Groups.Render != "dn" || cfg.Groups.As != "roles" {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
}

func TestFromYAMLGroupsRenderDefaultsAndValidation(t *testing.T) {
	cfg, err := FromYAML(decodeNode(t, "key: "+key32+"\ngroups:\n  source: isMemberOf\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Groups.Render != "cn" || cfg.Groups.As != "groups" {
		t.Fatalf("group defaults = %+v", cfg.Groups)
	}
	if _, err := FromYAML(decodeNode(t, "key: "+key32+"\ngroups:\n  source: x\n  render: bogus\n")); err == nil {
		t.Fatal("want error for invalid render")
	}
}
