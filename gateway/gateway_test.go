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
