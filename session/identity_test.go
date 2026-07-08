package session

import (
	"testing"

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
