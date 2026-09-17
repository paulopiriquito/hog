package session

import (
	"testing"
	"unicode/utf8"
)

func TestProjectPassportPrefersUserinfoOverIDToken(t *testing.T) {
	idClaims := map[string]any{"email": "id@x.co", "name": "ID Name"}
	userinfo := map[string]any{"email": "ui@x.co", "departmentnumber": "42"}
	got := projectPassport([]string{"email", "name", "departmentnumber", "absent"}, idClaims, userinfo)
	if got["email"] != "ui@x.co" {
		t.Errorf("email = %v", got["email"])
	}
	if got["name"] != "ID Name" {
		t.Errorf("name = %v", got["name"])
	}
	if got["departmentnumber"] != "42" {
		t.Errorf("dept = %v", got["departmentnumber"])
	}
	if _, ok := got["absent"]; ok {
		t.Errorf("absent claim should be skipped")
	}
}

func TestProjectGroupsFilterAndRender(t *testing.T) {
	userinfo := map[string]any{"isMemberOf": []any{
		"cn=APP-ROLE-myapp-admin,ou=myapp,ou=applicationRole,ou=role,o=example.com",
		"cn=APP-ROLE-myapp-viewer,ou=myapp,ou=applicationRole,ou=role,o=example.com",
		"cn=ALL-STAFF,ou=mailGroup,ou=messagingSystem,o=example.com",
		"cn=GLOBAL-ROLE-GITHUB-ACCESS,ou=GITHUB,ou=applicationRole,ou=role,ou=GLOBAL,O=EXAMPLE.COM",
	}}

	g := projectGroups(&GroupsConfig{
		Source: "isMemberOf", Match: []string{"ou=myapp,ou=applicationRole"}, Render: "cn", As: "groups",
	}, userinfo, nil)
	want := []string{"APP-ROLE-myapp-admin", "APP-ROLE-myapp-viewer"}
	if len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Fatalf("cn render = %v, want %v", g, want)
	}

	g2 := projectGroups(&GroupsConfig{
		Source: "isMemberOf", Match: []string{"OU=APPLICATIONROLE"}, Render: "dn", As: "groups",
	}, userinfo, nil)
	if len(g2) != 3 {
		t.Fatalf("dn render count = %d, want 3 (%v)", len(g2), g2)
	}
	if g2[0] != userinfo["isMemberOf"].([]any)[0].(string) {
		t.Fatalf("dn render should keep whole DN, got %q", g2[0])
	}
}

func TestProjectGroupsEmptyWhenSourceMissingOrNoMatch(t *testing.T) {
	if g := projectGroups(&GroupsConfig{Source: "isMemberOf", Match: []string{"ou=x"}, Render: "cn"}, map[string]any{}, nil); len(g) != 0 {
		t.Fatalf("missing source ⇒ empty, got %v", g)
	}
	ui := map[string]any{"isMemberOf": []any{"cn=foo,ou=bar"}}
	if g := projectGroups(&GroupsConfig{Source: "isMemberOf", Match: []string{"ou=nope"}, Render: "cn"}, ui, nil); len(g) != 0 {
		t.Fatalf("no match ⇒ empty, got %v", g)
	}
}

func TestProjectGroupsFallsBackToIdClaims(t *testing.T) {
	cfg := &GroupsConfig{Source: "isMemberOf", Match: []string{"ou=role"}, Render: "cn"}
	idClaims := map[string]any{"isMemberOf": []any{"cn=admins,ou=role"}}
	g := projectGroups(cfg, map[string]any{}, idClaims) // userinfo lacks the source ⇒ fall back to idClaims
	if len(g) != 1 || g[0] != "admins" {
		t.Fatalf("expected idClaims fallback to yield [admins], got %v", g)
	}
}

func TestProjectGroupsPrefersUserinfoOverIdClaims(t *testing.T) {
	cfg := &GroupsConfig{Source: "isMemberOf", Match: []string{"ou=role"}, Render: "cn"}
	ui := map[string]any{"isMemberOf": []any{"cn=ui-group,ou=role"}}
	id := map[string]any{"isMemberOf": []any{"cn=id-group,ou=role"}}
	g := projectGroups(cfg, ui, id) // both present ⇒ userinfo must win
	if len(g) != 1 || g[0] != "ui-group" {
		t.Fatalf("expected userinfo to win, got %v", g)
	}
}

func TestResolveSubject(t *testing.T) {
	if got := resolveSubject("", "fallback", map[string]any{"uid": "u"}, nil); got != "fallback" {
		t.Fatalf("empty claim: %q", got)
	}
	if got := resolveSubject("sub", "fallback", map[string]any{"uid": "u"}, nil); got != "fallback" {
		t.Fatalf("sub claim: %q", got)
	}
	if got := resolveSubject("uid", "fallback", map[string]any{"uid": "u"}, nil); got != "u" {
		t.Fatalf("uid from token: %q", got)
	}
	if got := resolveSubject("uid", "fallback", map[string]any{"uid": 42}, nil); got != "fallback" {
		t.Fatalf("non-string claim must fall back: %q", got)
	}
	if got := resolveSubject("uid", "fallback", map[string]any{"uid": "token"}, map[string]any{"uid": "userinfo"}); got != "userinfo" {
		t.Fatalf("userinfo must win over the token claim: %q", got)
	}
}

func TestProjectGroupsStripsConfiguredPrefixes(t *testing.T) {
	userinfo := map[string]any{"memberof": []any{
		"cn=GLOBAL-ROLE-myapp-myapp.owner,ou=myapp,ou=applicationRole,ou=role,ou=GLOBAL,O=EXAMPLE.COM",
		"cn=APP-ROLE-myapp-myapp.editor,ou=myapp,ou=applicationRole,ou=role,o=example.com",
		"cn=APP-ROLE-billing-users,ou=billing,ou=applicationRole,ou=role,o=example.com",
	}}
	g := projectGroups(&GroupsConfig{
		Source: "memberof", Match: []string{"ou=myapp,ou=applicationrole"}, Render: "cn", As: "groups",
		Strip: []string{"app-role-myapp-", "GLOBAL-ROLE-myapp-"},
	}, userinfo, nil)
	want := []string{"myapp.owner", "myapp.editor"}
	if len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Fatalf("stripped groups = %v, want %v", g, want)
	}
	if got := stripPrefixes("cn-only", []string{"x-"}); got != "cn-only" {
		t.Fatalf("no-op strip changed the value: %q", got)
	}
	if got := stripPrefixes("AB-value", []string{"ab-", "a"}); got != "value" {
		t.Fatalf("first matching prefix must win: %q", got)
	}
}

func TestStripPrefixesEdgeCases(t *testing.T) {
	// A prefix equal to the whole value strips it to "" — projectGroups' existing
	// non-empty guard must drop that group rather than emit an empty one.
	ui := map[string]any{"memberof": []any{
		"cn=APP-ROLE-myapp-,ou=myapp,ou=applicationRole,ou=role,o=example.com",
		"cn=APP-ROLE-myapp-owner,ou=myapp,ou=applicationRole,ou=role,o=example.com",
	}}
	g := projectGroups(&GroupsConfig{
		Source: "memberof", Match: []string{"ou=myapp,ou=applicationrole"}, Render: "cn",
		Strip: []string{"app-role-myapp-"},
	}, ui, nil)
	if len(g) != 1 || g[0] != "owner" {
		t.Fatalf("whole-value prefix must yield an empty group dropped by projectGroups, got %v", g)
	}

	// An empty entry ahead of a real prefix in the list must be skipped, not
	// treated as a prefix that matches everything (which would leave v unchanged).
	if got := stripPrefixes("APP-ROLE-myapp-owner", []string{"", "app-role-myapp-"}); got != "owner" {
		t.Fatalf("empty prefix entry must be skipped, not match everything: %q", got)
	}

	// Non-ASCII: U+212A KELVIN SIGN (3 bytes) case-folds to ASCII 'k' (1 byte), so
	// the byte offset consumed in v cannot be len(p) — it must come from walking v's
	// own runes.
	value := "Kelvin-role"
	got := stripPrefixes(value, []string{"Kelvin-"})
	if got != "role" {
		t.Fatalf("kelvin-sign prefix strip = %q, want %q", got, "role")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("result must be valid UTF-8, got %q", got)
	}
}
