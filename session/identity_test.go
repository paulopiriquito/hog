package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/v2/idp"
	"gopkg.in/yaml.v3"
)

func idNode(t *testing.T, s string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	// yaml.Unmarshal yields a document node; descend to the mapping.
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return *n.Content[0]
	}
	return n
}

func TestParseIdentityDefaults(t *testing.T) {
	cfg, err := ParseIdentity(yaml.Node{}) // zero node ⇒ all defaults
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"email", "name", "given_name", "family_name"}
	if len(cfg.Claims) != len(want) {
		t.Fatalf("default claims = %v", cfg.Claims)
	}
	for i := range want {
		if cfg.Claims[i] != want[i] {
			t.Fatalf("default claims = %v", cfg.Claims)
		}
	}
	if cfg.Groups != nil {
		t.Error("groups should be nil when unconfigured")
	}
	if cfg.UserInfo != "auto" {
		t.Fatalf("default userInfo = %q", cfg.UserInfo)
	}
}

func TestParseIdentityExplicitEmptyClaims(t *testing.T) {
	cfg, err := ParseIdentity(idNode(t, "claims: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Claims) != 0 {
		t.Fatalf("explicit [] ⇒ no claims, got %v", cfg.Claims)
	}
}

func TestParseIdentityGroupsAndOverrides(t *testing.T) {
	cfg, err := ParseIdentity(idNode(t, `
claims: [email, departmentnumber]
groups:
  source: isMemberOf
  match: [ou=applicationRole]
  render: dn
  as: roles
userInfo: always
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Groups == nil || cfg.Groups.Source != "isMemberOf" || cfg.Groups.Render != "dn" || cfg.Groups.As != "roles" {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
	if cfg.UserInfo != "always" {
		t.Fatalf("userInfo = %q", cfg.UserInfo)
	}
	if len(cfg.Claims) != 2 || cfg.Claims[0] != "email" || cfg.Claims[1] != "departmentnumber" {
		t.Fatalf("claims = %v", cfg.Claims)
	}
	if cfg.Groups == nil || len(cfg.Groups.Match) != 1 || cfg.Groups.Match[0] != "ou=applicationRole" {
		t.Fatalf("groups.match = %+v", cfg.Groups)
	}
}

func TestParseIdentityGroupsRenderDefaultsAndValidation(t *testing.T) {
	cfg, err := ParseIdentity(idNode(t, "groups:\n  source: isMemberOf\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Groups.Render != "cn" || cfg.Groups.As != "groups" {
		t.Fatalf("group defaults = %+v", cfg.Groups)
	}
	if _, err := ParseIdentity(idNode(t, "groups:\n  source: x\n  render: bogus\n")); err == nil {
		t.Fatal("want error for invalid render")
	}
	if _, err := ParseIdentity(idNode(t, "userInfo: maybe\n")); err == nil {
		t.Fatal("want error for invalid userInfo")
	}
}

func TestNewPrincipalProjects(t *testing.T) {
	idCfg := IdentityConfig{
		Claims: []string{"email"},
		Groups: &GroupsConfig{Source: "isMemberOf", Match: []string{"ou=applicationRole"}, Render: "cn", As: "groups"},
	}
	idClaims := map[string]any{"email": "tok@x.co"}
	ui := map[string]any{"isMemberOf": []any{"cn=admins,ou=applicationRole"}}
	p := NewPrincipal("u-1", idClaims, ui, "at", idCfg)
	if p.Subject != "u-1" || p.AccessToken != "at" {
		t.Fatalf("principal = %+v", p)
	}
	if p.Passport["email"] != "tok@x.co" {
		t.Fatalf("passport = %v", p.Passport)
	}
	if len(p.Groups) != 1 || p.Groups[0] != "admins" {
		t.Fatalf("groups = %v", p.Groups)
	}

	// token-only: nil userinfo ⇒ passport falls back to token claims, no groups
	p2 := NewPrincipal("u-2", map[string]any{"email": "tok@x.co"}, nil, "at", idCfg)
	if p2.Passport["email"] != "tok@x.co" {
		t.Fatalf("token-only passport = %v", p2.Passport)
	}
	if len(p2.Groups) != 0 {
		t.Fatalf("token-only groups should be empty, got %v", p2.Groups)
	}
}

func TestNeedUserInfoForToken(t *testing.T) {
	groups := &GroupsConfig{Source: "isMemberOf"}
	cases := []struct {
		name   string
		cfg    IdentityConfig
		claims map[string]any
		want   bool
	}{
		{"never", IdentityConfig{UserInfo: "never", Groups: groups}, nil, false},
		{"always", IdentityConfig{UserInfo: "always"}, map[string]any{"email": "x"}, true},
		{"auto-groups-missing", IdentityConfig{UserInfo: "auto", Groups: groups}, map[string]any{}, true},
		{"auto-groups-present", IdentityConfig{UserInfo: "auto", Groups: groups, Claims: []string{"email"}}, map[string]any{"isMemberOf": []any{"x"}, "email": "e"}, false},
		{"auto-claim-missing", IdentityConfig{UserInfo: "auto", Claims: []string{"email", "departmentnumber"}}, map[string]any{"email": "e"}, true},
		{"auto-all-present", IdentityConfig{UserInfo: "auto", Claims: []string{"email"}}, map[string]any{"email": "e"}, false},
		{"auto-nothing-configured", IdentityConfig{UserInfo: "auto"}, map[string]any{"x": "y"}, false},
	}
	for _, c := range cases {
		if got := NeedUserInfoForToken(c.cfg, c.claims); got != c.want {
			t.Errorf("%s: NeedUserInfoForToken = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseIdentitySubjectClaimDefaultsToSub(t *testing.T) {
	cfg, err := ParseIdentity(yaml.Node{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SubjectClaim != "sub" {
		t.Fatalf("default subjectClaim = %q, want sub", cfg.SubjectClaim)
	}
	cfg, err = ParseIdentity(idNode(t, "subjectClaim: uid\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SubjectClaim != "uid" {
		t.Fatalf("subjectClaim = %q", cfg.SubjectClaim)
	}
}

func TestNewPrincipalUsesConfiguredSubjectClaim(t *testing.T) {
	idCfg := IdentityConfig{Claims: []string{"email"}, SubjectClaim: "uid"}
	p := NewPrincipal("", map[string]any{"uid": "u-10427", "email": "p@example.com"}, nil, "at", idCfg)
	if p.Subject != "u-10427" {
		t.Fatalf("subject = %q, want the uid claim", p.Subject)
	}
	// userinfo wins over the token, like every other claim
	p = NewPrincipal("", map[string]any{"uid": "token"}, map[string]any{"uid": "userinfo"}, "at", idCfg)
	if p.Subject != "userinfo" {
		t.Fatalf("subject = %q, want the userinfo value", p.Subject)
	}
	// claim absent ⇒ fall back to the token subject (never silently empty)
	p = NewPrincipal("sub-1", map[string]any{"email": "p@x.co"}, nil, "at", idCfg)
	if p.Subject != "sub-1" {
		t.Fatalf("subject = %q, want the sub fallback", p.Subject)
	}
}

func TestNeedUserInfoForTokenWhenSubjectClaimMissing(t *testing.T) {
	idCfg := IdentityConfig{Claims: []string{}, SubjectClaim: "uid", UserInfo: "auto"}
	if !NeedUserInfoForToken(idCfg, map[string]any{"sub": "x"}) {
		t.Fatal("a token without the subject claim must trigger userinfo")
	}
	if NeedUserInfoForToken(idCfg, map[string]any{"uid": "u-10427"}) {
		t.Fatal("a token carrying the subject claim needs no userinfo")
	}
	if !NeedUserInfoForToken(idCfg, map[string]any{"uid": ""}) {
		t.Fatal("an empty-string subject claim is as good as absent and must trigger userinfo")
	}
	if !NeedUserInfoForToken(idCfg, map[string]any{"uid": 42}) {
		t.Fatal("a non-string subject claim is as good as absent and must trigger userinfo")
	}
}

func TestMakeSessionUsesConfiguredSubjectClaim(t *testing.T) {
	cfg := Config{SubjectClaim: "uid", TTL: time.Hour}
	idt := &idp.Identity{Subject: "sub-1", Claims: map[string]any{"uid": "u-10427"}}
	req := httptest.NewRequest("GET", "/", nil)
	s := makeSession(cfg, idt, nil, &idp.Tokens{AccessToken: "at"}, req)
	if s.Subject != "u-10427" {
		t.Fatalf("session subject = %q", s.Subject)
	}
}

func TestParseIdentityAssertionFullBlock(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seedB64 := base64.StdEncoding.EncodeToString(priv.Seed())
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	cfg, err := ParseIdentity(idNode(t, fmt.Sprintf(`
assertion:
  issue:
    name: go-app
    keyId: k1
    key: %s
  accept:
    issuer: go-app
    keys:
      - keyId: k1
        publicKey: %s
`, seedB64, pubB64)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Assertion == nil || cfg.Assertion.Issue == nil || cfg.Assertion.Accept == nil {
		t.Fatalf("assertion = %+v", cfg.Assertion)
	}
	if cfg.Assertion.Issue.TTL != 60*time.Second {
		t.Fatalf("issue.TTL = %v, want 60s default", cfg.Assertion.Issue.TTL)
	}
	if cfg.Assertion.Issue.Header != "X-Hog-Identity" {
		t.Fatalf("issue.Header = %q", cfg.Assertion.Issue.Header)
	}
	if cfg.Assertion.Accept.Header != "X-Hog-Identity" {
		t.Fatalf("accept.Header = %q", cfg.Assertion.Accept.Header)
	}
	if !cfg.Assertion.Accept.RequireBearer {
		t.Fatal("requireBearer should default true")
	}
	if len(cfg.Assertion.Issue.Seed) != ed25519.SeedSize {
		t.Fatalf("issue.Seed length = %d, want %d", len(cfg.Assertion.Issue.Seed), ed25519.SeedSize)
	}
	if got := cfg.Assertion.Accept.Keys["k1"]; len(got) != ed25519.PublicKeySize {
		t.Fatalf("accept.Keys[k1] length = %d, want %d", len(got), ed25519.PublicKeySize)
	}
}

func TestParseIdentityAssertionRejectsShortSeed(t *testing.T) {
	bad := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := ParseIdentity(idNode(t, fmt.Sprintf(`
assertion:
  issue:
    name: go-app
    keyId: k1
    key: %s
`, bad)))
	if err == nil {
		t.Fatal("want error for a 16-byte seed")
	}
}

func TestParseIdentityAssertionRejectsShortPublicKey(t *testing.T) {
	bad := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := ParseIdentity(idNode(t, fmt.Sprintf(`
assertion:
  accept:
    issuer: go-app
    keys:
      - keyId: k1
        publicKey: %s
`, bad)))
	if err == nil {
		t.Fatal("want error for a 16-byte public key")
	}
}

func TestParseIdentityAssertionRejectsAcceptWithNoKeys(t *testing.T) {
	_, err := ParseIdentity(idNode(t, "assertion:\n  accept:\n    issuer: go-app\n"))
	if err == nil {
		t.Fatal("want error for accept with no keys")
	}
}

func TestParseIdentityAssertionRejectsIssueWithoutName(t *testing.T) {
	seed := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))
	_, err := ParseIdentity(idNode(t, fmt.Sprintf("assertion:\n  issue:\n    keyId: k1\n    key: %s\n", seed)))
	if err == nil {
		t.Fatal("want error for issue without name")
	}
}

func TestParseIdentityAssertionRejectsDuplicateKids(t *testing.T) {
	pub := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	_, err := ParseIdentity(idNode(t, fmt.Sprintf(`
assertion:
  accept:
    issuer: go-app
    keys:
      - keyId: k1
        publicKey: %s
      - keyId: k1
        publicKey: %s
`, pub, pub)))
	if err == nil {
		t.Fatal("want error for duplicate kids")
	}
}

func TestParseIdentityAssertionRejectsEmptyBlock(t *testing.T) {
	_, err := ParseIdentity(idNode(t, "assertion: {}\n"))
	if err == nil {
		t.Fatal("want error for an assertion block with neither issue nor accept")
	}
}

func TestParseIdentityAssertionHonoursExplicitOverrides(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seedB64 := base64.StdEncoding.EncodeToString(priv.Seed())
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	cfg, err := ParseIdentity(idNode(t, fmt.Sprintf(`
assertion:
  issue:
    name: go-app
    keyId: k1
    key: %s
    ttl: 5s
    header: X-Other
  accept:
    issuer: go-app
    header: X-Other
    requireBearer: false
    keys:
      - keyId: k1
        publicKey: %s
`, seedB64, pubB64)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Assertion.Issue.TTL != 5*time.Second {
		t.Fatalf("issue.TTL = %v, want 5s", cfg.Assertion.Issue.TTL)
	}
	if cfg.Assertion.Issue.Header != "X-Other" {
		t.Fatalf("issue.Header = %q, want X-Other", cfg.Assertion.Issue.Header)
	}
	if cfg.Assertion.Accept.Header != "X-Other" {
		t.Fatalf("accept.Header = %q, want X-Other", cfg.Assertion.Accept.Header)
	}
	if cfg.Assertion.Accept.RequireBearer {
		t.Fatal("requireBearer should be false when explicitly set")
	}
}

func TestParseIdentityGroupsStrip(t *testing.T) {
	cfg, err := ParseIdentity(idNode(t, "groups:\n  source: memberof\n  match: [\"ou=myapp\"]\n  strip: [\"APP-ROLE-myapp-\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups.Strip) != 1 || cfg.Groups.Strip[0] != "APP-ROLE-myapp-" {
		t.Fatalf("strip = %v", cfg.Groups.Strip)
	}
}
