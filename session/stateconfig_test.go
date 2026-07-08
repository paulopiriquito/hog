package session

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func spNode(t *testing.T, s string) yaml.Node {
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

func TestParseStateProviderDefaults(t *testing.T) {
	c, err := ParseStateProvider(spNode(t, "type: valkey\nconfig: { address: localhost:6379 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "valkey" {
		t.Fatalf("type = %q", c.Type)
	}
	if c.RefreshSkew != 60*time.Second {
		t.Fatalf("refreshSkew default = %v", c.RefreshSkew)
	}
	if c.KeyPrefix != "hog:sess:" {
		t.Fatalf("keyPrefix default = %q", c.KeyPrefix)
	}
	if c.Config.Kind == 0 {
		t.Fatal("opaque config node not captured")
	}
}

func TestParseStateProviderOverridesAndValidation(t *testing.T) {
	c, err := ParseStateProvider(spNode(t, "type: redis\nrefreshSkew: 30s\nkeyPrefix: 'x:'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RefreshSkew != 30*time.Second || c.KeyPrefix != "x:" {
		t.Fatalf("overrides = %+v", c)
	}
	if _, err := ParseStateProvider(spNode(t, "refreshSkew: 30s\n")); err == nil {
		t.Fatal("want error when type is missing")
	}
	if _, err := ParseStateProvider(spNode(t, "type: x\nrefreshSkew: nonsense\n")); err == nil {
		t.Fatal("want error for invalid refreshSkew")
	}
	if _, err := ParseStateProvider(spNode(t, "type: x\nrefreshSkew: -5s\n")); err == nil {
		t.Fatal("want error for negative refreshSkew")
	}
}
