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
	if c.LoginPath != "/auth/login" || c.LogoutPath != "/auth/logout" || c.UserInfo != "auto" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestConfigOverridesAndValidation(t *testing.T) {
	c, err := FromYAML(node(t, "loginPath: /signin\nlogoutPath: /signout\nuserInfo: always\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LoginPath != "/signin" || c.LogoutPath != "/signout" || c.UserInfo != "always" {
		t.Fatalf("overrides = %+v", c)
	}
	if _, err := FromYAML(node(t, "userInfo: bogus\n")); err == nil {
		t.Fatal("want error for invalid userInfo mode")
	}
}

func TestNeedsUserInfo(t *testing.T) {
	defaults := session.Config{PassportClaims: []string{"email", "name", "given_name", "family_name"}}
	withGroups := session.Config{PassportClaims: defaults.PassportClaims, Groups: &session.GroupsConfig{Source: "isMemberOf"}}
	withExtra := session.Config{PassportClaims: []string{"email", "departmentnumber"}}

	if NeedsUserInfo(defaults, "auto") {
		t.Error("default claims, no groups ⇒ no fetch")
	}
	if !NeedsUserInfo(withGroups, "auto") {
		t.Error("groups configured ⇒ fetch")
	}
	if !NeedsUserInfo(withExtra, "auto") {
		t.Error("non-default claim ⇒ fetch")
	}
	if !NeedsUserInfo(defaults, "always") {
		t.Error("always ⇒ fetch")
	}
	if NeedsUserInfo(withGroups, "never") {
		t.Error("never ⇒ no fetch")
	}
}
