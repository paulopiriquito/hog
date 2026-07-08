package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/terminal"
)

func TestParse(t *testing.T) {
	rs, err := config.DecodeAll([]byte(`
kind: Gateway
metadata: { name: hog }
spec: { listen: ":9999" }
---
kind: Route
metadata: { name: hc, labels: { tier: system } }
spec: { match: /health, handler: { type: health } }
---
kind: RouteGroup
metadata: { name: g }
spec: { selector: { matchLabels: { tier: system } }, policy: { auth: required } }
---
kind: RequestPlugin
metadata: { name: rp1 }
spec: { type: tap, selector: { matchLabels: { tier: system } }, config: {} }
---
kind: ResponsePlugin
metadata: { name: sp1 }
spec: { type: shape, selector: {}, config: {} }
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(rs)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Gateway.Listen != ":9999" {
		t.Fatalf("listen = %q", cfg.Gateway.Listen)
	}
	if len(cfg.Routes) != 1 || len(cfg.Groups) != 1 {
		t.Fatalf("routes=%d groups=%d", len(cfg.Routes), len(cfg.Groups))
	}
	if len(cfg.RequestPlugins) != 1 || cfg.RequestPlugins[0].Type != "tap" {
		t.Fatalf("request plugins = %+v", cfg.RequestPlugins)
	}
	if len(cfg.ResponsePlugins) != 1 || cfg.ResponsePlugins[0].Order != 4 {
		t.Fatalf("response plugin order = %+v (want document index 4)", cfg.ResponsePlugins)
	}
}

func TestParseRequiresGateway(t *testing.T) {
	rs := mustDecode(t, "kind: Route\nmetadata: {name: r}\nspec: {match: /x, handler: {type: health}}\n")
	if _, err := Parse(rs); err == nil {
		t.Fatal("want error when no Gateway resource present")
	}
}

func TestParseRejectsDuplicateGateway(t *testing.T) {
	rs := mustDecode(t, "kind: Gateway\nmetadata: {name: g1}\nspec: {}\n---\nkind: Gateway\nmetadata: {name: g2}\nspec: {}\n")
	if _, err := Parse(rs); err == nil {
		t.Fatal("want error on duplicate Gateway")
	}
}

func TestBuildWiresSelectorsAndSlots(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)

	// Fake request-plugin: records that it ran (request side).
	var ran []string
	reg.Register("RequestPlugin", "tap", func(string, registry.RawConfig) (any, error) {
		return chain.Func(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if w.Header().Get("X-Request-Id") == "" {
					t.Error("skeleton (request-id) must run before request-plugins")
				}
				ran = append(ran, "tap")
				next.ServeHTTP(w, r)
			})
		}), nil
	})
	// Fake response-plugin: wraps the body.
	reg.Register("ResponsePlugin", "shape", func(string, registry.RawConfig) (any, error) {
		return chain.Func(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				buf := chain.NewBuffer(w)
				next.ServeHTTP(buf, r)
				w.WriteHeader(buf.Status())
				io.WriteString(w, "["+string(buf.Body())+"]")
			})
		}), nil
	})

	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: hc, labels: { tier: system } }
spec: { match: /health, handler: { type: health } }
---
kind: RequestPlugin
metadata: { name: rp }
spec: { type: tap, selector: { matchLabels: { tier: system } } }
---
kind: ResponsePlugin
metadata: { name: sp }
spec: { type: shape, selector: {} }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Join(ran, ",") != "tap" {
		t.Fatalf("request plugin did not run: %v", ran)
	}
	if rec.Body.String() != `[{"status":"ok"}]` {
		t.Fatalf("response plugin did not shape body: %q", rec.Body.String())
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("built-in skeleton (request-id) did not run")
	}
}

func TestBuildRejectsDuplicateMatch(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: a }
spec: { match: /dup, handler: { type: health } }
---
kind: Route
metadata: { name: b }
spec: { match: /dup, handler: { type: health } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("want error on duplicate route match")
	}
}

func mustDecode(t *testing.T, s string) []config.Resource {
	t.Helper()
	rs, err := config.DecodeAll([]byte(s))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rs
}

func TestBuildInstantiatesSingleIdP(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	idp.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+fakeIssuer(t)+`, clientID: c, clientSecret: s, redirectURL: https://app/cb }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if a.IdP == nil {
		t.Fatal("App.IdP not set")
	}
}

func TestBuildRejectsMultipleIdP(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	idp.Register(reg)
	iss := fakeIssuer(t)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: IdP
metadata: { name: a }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app/cb }
---
kind: IdP
metadata: { name: b }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app/cb }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("want error for multiple IdP resources")
	}
}

func TestParseRejectsUnknownKind(t *testing.T) {
	rs := mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec: {}\n---\nkind: Wat\nmetadata: {name: x}\nspec: {}\n")
	if _, err := Parse(rs); err == nil {
		t.Fatal("want error for unknown resource kind")
	}
}

func TestBuildConstructsSessionManager(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session:
    key: "0123456789abcdef0123456789abcdef"
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if a.Session == nil {
		t.Fatal("App.Session not constructed")
	}
}

func TestBuildNoSessionBlockLeavesNilManager(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Session != nil {
		t.Fatal("expected nil Session manager when no session block")
	}
}

func TestBuildFailsFastOnBadSessionKey(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec:\n  session:\n    key: short\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("want fail-fast on bad session key")
	}
}

func TestBuildMountsAuthEndpoints(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	idp.Register(reg)
	iss := fakeIssuer(t)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session:
    key: "0123456789abcdef0123456789abcdef"
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app.example/auth/callback }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/login", nil)
	req.Header.Set("User-Agent", "UA")
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("/auth/login status = %d, want 302", rec.Code)
	}
	rec2 := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec2, httptest.NewRequest("GET", "/auth/session", nil))
	if rec2.Code == http.StatusNotFound {
		t.Fatal("/auth/session not mounted")
	}
}

func TestBuildNoAuthEndpointsWithoutSession(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	idp.Register(reg)
	iss := fakeIssuer(t)
	cfg, _ := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app.example/auth/callback }
`))
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/auth/login without session should be 404, got %d", rec.Code)
	}
}

// fakeIssuer stands up a throwaway OIDC discovery+JWKS server so the IdP
// factory's eager discovery succeeds.
func fakeIssuer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/a","token_endpoint":"` + srv.URL + `/t","jwks_uri":"` + srv.URL + `/jwks"}`))
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	return srv.URL
}
