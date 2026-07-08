package gateway

import (
	"testing"

	"github.com/paulopiriquito/hog/config"
)

func TestFromResource(t *testing.T) {
	rs, err := config.DecodeAll([]byte(`
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  otelPort: ":9090"
  trustedProxies: [ "10.0.0.0/8" ]
  plugins: [ "github.com/acme/hogx/geogate" ]
`))
	if err != nil {
		t.Fatal(err)
	}
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatalf("FromResource: %v", err)
	}
	if g.Listen != ":8080" || g.OTELPort != ":9090" {
		t.Fatalf("addrs = %q %q", g.Listen, g.OTELPort)
	}
	if len(g.TrustedProxies) != 1 || g.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("trustedProxies = %v", g.TrustedProxies)
	}
	if len(g.Plugins) != 1 {
		t.Fatalf("plugins = %v", g.Plugins)
	}
}

func TestDefaults(t *testing.T) {
	rs, _ := config.DecodeAll([]byte("kind: Gateway\nmetadata: {name: hog}\nspec: {}\n"))
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatal(err)
	}
	if g.Listen != ":8080" {
		t.Fatalf("default listen = %q, want :8080", g.Listen)
	}
}

func TestFromResourceCapturesSessionBlock(t *testing.T) {
	rs, err := config.DecodeAll([]byte(`
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  session:
    key: "0123456789abcdef0123456789abcdef"
    ttl: 2h
`))
	if err != nil {
		t.Fatal(err)
	}
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatalf("FromResource: %v", err)
	}
	if g.Session.Kind == 0 {
		t.Fatal("session block not captured")
	}
}

func TestFromResourceCapturesAuthBlock(t *testing.T) {
	rs, _ := config.DecodeAll([]byte("kind: Gateway\nmetadata: {name: hog}\nspec:\n  auth:\n    loginPath: /signin\n"))
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatal(err)
	}
	if g.Auth.Kind == 0 {
		t.Fatal("auth block not captured")
	}
}

func TestFromResourceCapturesIdentityBlock(t *testing.T) {
	rs, _ := config.DecodeAll([]byte("kind: Gateway\nmetadata: {name: hog}\nspec:\n  identity:\n    claims: [email]\n"))
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatalf("FromResource: %v", err)
	}
	if g.Identity.Kind == 0 {
		t.Fatal("identity node not captured")
	}
}

func TestFromResourceCapturesStateProviderBlock(t *testing.T) {
	rs, _ := config.DecodeAll([]byte("kind: Gateway\nmetadata: {name: hog}\nspec:\n  stateProvider:\n    type: valkey\n"))
	g, err := FromResource(rs[0])
	if err != nil {
		t.Fatalf("FromResource: %v", err)
	}
	if g.StateProvider.Kind == 0 {
		t.Fatal("stateProvider node not captured")
	}
}
