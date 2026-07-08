package auth

import (
	"testing"

	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

func node(t *testing.T, s string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return *n.Content[0]
	}
	return n
}

func TestConfigDefaults(t *testing.T) {
	var empty yaml.Node // zero node ⇒ all defaults
	c, err := FromYAML(empty)
	if err != nil {
		t.Fatal(err)
	}
	if c.LoginPath != "/auth/login" || c.LogoutPath != "/auth/logout" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestConfigOverrides(t *testing.T) {
	c, err := FromYAML(node(t, "loginPath: /signin\nlogoutPath: /signout\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LoginPath != "/signin" || c.LogoutPath != "/signout" {
		t.Fatalf("overrides = %+v", c)
	}
}

func TestNeedsUserInfo(t *testing.T) {
	defaults := session.IdentityConfig{Claims: []string{"email", "name", "given_name", "family_name"}, UserInfo: "auto"}
	withGroups := session.IdentityConfig{Claims: defaults.Claims, Groups: &session.GroupsConfig{Source: "isMemberOf"}, UserInfo: "auto"}
	withExtra := session.IdentityConfig{Claims: []string{"email", "departmentnumber"}, UserInfo: "auto"}
	always := session.IdentityConfig{Claims: defaults.Claims, UserInfo: "always"}
	never := session.IdentityConfig{Groups: &session.GroupsConfig{Source: "isMemberOf"}, UserInfo: "never"}

	if NeedsUserInfo(defaults) {
		t.Error("default claims, no groups ⇒ no fetch")
	}
	if !NeedsUserInfo(withGroups) {
		t.Error("groups configured ⇒ fetch")
	}
	if !NeedsUserInfo(withExtra) {
		t.Error("non-default claim ⇒ fetch")
	}
	if !NeedsUserInfo(always) {
		t.Error("always ⇒ fetch")
	}
	if NeedsUserInfo(never) {
		t.Error("never ⇒ no fetch")
	}
}
