package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/terminal"
)

// Full spine: load resources -> Parse -> Build -> serve, with only built-in
// modules (health terminal + skeleton). Proves an end-to-end request works.
func TestEndToEndHealth(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)

	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: { listen: ":0" }
---
kind: Route
metadata: { name: hc, labels: { tier: system } }
spec: { match: /healthz, handler: { type: health } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Fatal("skeleton did not run end-to-end")
	}
}
