package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/session"
	"github.com/paulopiriquito/hog/terminal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
spec: { selector: { matchLabels: { tier: system } }, access: { auth: required } }
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

func TestParseTelemetryDefaultsWhenAbsent(t *testing.T) {
	cfg, err := Parse(mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Service.Name != "hog" || cfg.Telemetry.LogLevel != "info" {
		t.Fatalf("telemetry defaults = %+v", cfg.Telemetry)
	}
}

func TestParseTelemetryResource(t *testing.T) {
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Telemetry
metadata: { name: t }
spec:
  service: { name: edge }
  accessLog: { properties: [method, session_id] }
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Service.Name != "edge" {
		t.Fatalf("service = %q", cfg.Telemetry.Service.Name)
	}
	if len(cfg.Telemetry.AccessLog.Properties) != 2 {
		t.Fatalf("properties = %v", cfg.Telemetry.AccessLog.Properties)
	}
}

func TestParseRejectsDuplicateTelemetry(t *testing.T) {
	_, err := Parse(mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec: {}\n---\nkind: Telemetry\nmetadata: {name: a}\nspec: {service: {name: x}}\n---\nkind: Telemetry\nmetadata: {name: b}\nspec: {service: {name: y}}\n"))
	if err == nil {
		t.Fatal("want error on duplicate Telemetry")
	}
}

func TestParseRejectsTelemetryMissingService(t *testing.T) {
	_, err := Parse(mustDecode(t, "kind: Gateway\nmetadata: {name: hog}\nspec: {}\n---\nkind: Telemetry\nmetadata: {name: a}\nspec: {logLevel: info}\n"))
	if err == nil {
		t.Fatal("want error when service.name missing")
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

// Logout is POST-only (state-changing) and same-origin (via the gateway-wide
// security stage): a GET is 405, a cross-origin POST is 403, a same-origin/
// non-browser POST proceeds. This closes cross-site forced-logout.
func TestBuildLogoutRequiresPostSameOrigin(t *testing.T) {
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

	// GET is rejected by the mux (method-restricted route) with 405.
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/logout", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /auth/logout = %d, want 405", rec.Code)
	}

	// A cross-origin POST is rejected by the security stage's CSRF check.
	recX := httptest.NewRecorder()
	reqX := httptest.NewRequest("POST", "/auth/logout", nil)
	reqX.Header.Set("Sec-Fetch-Site", "cross-site")
	a.Handler.ServeHTTP(recX, reqX)
	if recX.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /auth/logout = %d, want 403", recX.Code)
	}

	// A same-origin / non-browser POST (no Sec-Fetch-Site) proceeds to the handler,
	// which clears the session and redirects to the post-logout page.
	recOK := httptest.NewRecorder()
	a.Handler.ServeHTTP(recOK, httptest.NewRequest("POST", "/auth/logout", nil))
	if recOK.Code != http.StatusFound {
		t.Fatalf("same-origin POST /auth/logout = %d, want 302", recOK.Code)
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

// registerEcho registers a terminal that writes the projected X-User-Id + X-User-Groups,
// so a test can observe what the projection gate injected onto the backend-bound request.
func registerEcho(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "echo-user", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Header.Get("X-User-Id")+"|"+r.Header.Get("X-User-Groups"))
		}), nil
	})
}

func mintSessionCookie(t *testing.T, key string) []*http.Cookie {
	t.Helper()
	m, err := session.NewManager(session.Config{
		CookieName: "hog_session", Key: []byte(key), TTL: time.Hour,
		FingerprintHeaders: []string{"User-Agent"}, PassportClaims: []string{"email"},
		Groups: &session.GroupsConfig{Source: "isMemberOf", Match: []string{"ou=applicationRole"}, Render: "cn", As: "groups"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	ui := map[string]any{"email": "a@b.co", "isMemberOf": []any{"cn=admins,ou=applicationRole"}}
	if err := m.Issue(wr, rq, &idp.Identity{Subject: "u-1"}, ui, &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return wr.Result().Cookies()
}

const testKey = "0123456789abcdef0123456789abcdef"

func TestEnforcementAppRedirectsUnauth(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Route
metadata: { name: spa, labels: { x: y } }
spec:
  match: /app/
  type: app
  handler: { type: echo-user }
  access: { auth: required }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/app/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if rec.Header().Get("Location") != "/auth/login?return_to=%2Fapp%2F" {
		t.Fatalf("location = %q", rec.Header().Get("Location"))
	}
}

func TestEnforcementServiceUnauth401(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  type: service
  handler: { type: echo-user }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestEnforcementAuthedProjects(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Route
metadata: { name: spa, labels: { x: y } }
spec:
  match: /app/
  type: app
  handler: { type: echo-user }
  access: { auth: required }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/app/", nil)
	req.Header.Set("User-Agent", "UA")
	req.Header.Set("X-User-Id", "SPOOF") // must be stripped
	for _, c := range mintSessionCookie(t, testKey) {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("authed status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "u-1|admins" {
		t.Fatalf("projected body = %q, want \"u-1|admins\"", rec.Body.String())
	}
}

func TestBuildValidatesRouteTypeWithoutSession(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: bad }
spec:
  match: /x/
  type: totally-bogus
  handler: { type: echo-user }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("Build must fail on invalid route type even without a session manager")
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

func TestEnforcementPublicAppRouteServedUnauth(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Route
metadata: { name: spa }
spec:
  match: /pub/
  type: app
  handler: { type: echo-user }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/pub/", nil))
	if rec.Code != 200 {
		t.Fatalf("public app route status = %d, want 200", rec.Code)
	}
	// unauthenticated ⇒ no identity projected
	if rec.Body.String() != "|" {
		t.Fatalf("public unauth body = %q, want \"|\"", rec.Body.String())
	}
}

func TestEnforcementServiceAuthedProjects(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  type: service
  handler: { type: echo-user }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range mintSessionCookie(t, testKey) {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("authed service status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "u-1|admins" {
		t.Fatalf("service projected body = %q, want \"u-1|admins\"", rec.Body.String())
	}
}

// registerEchoBearer writes "X-User-Id|X-User-Groups|Authorization" so a test can
// assert both the projected identity and that the inbound Authorization survived.
func registerEchoBearer(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "echo-bearer", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Header.Get("X-User-Id")+"|"+r.Header.Get("X-User-Groups")+"|"+r.Header.Get("Authorization"))
		}), nil
	})
}

// newBearerIdP stands up a fake OIDC provider serving a real JWKS + userinfo, and
// returns its issuer URL and a signer for access tokens (aud defaults to clientID).
func newBearerIdP(t *testing.T, clientID string) (issuer string, signAccess func(claims map[string]any) string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &priv.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/a", "token_endpoint": srv.URL + "/t",
			"jwks_uri": srv.URL + "/jwks", "userinfo_endpoint": srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(jwks) })
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "user-123", "isMemberOf": []string{"cn=admins,ou=applicationRole"}})
	})
	signAccess = func(claims map[string]any) string {
		c := map[string]any{"iss": srv.URL, "aud": clientID, "sub": "user-123",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		for k, v := range claims {
			c[k] = v
		}
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: priv, KeyID: "k1"}},
			(&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(c)
		jws, err := signer.Sign(payload)
		if err != nil {
			t.Fatal(err)
		}
		s, err := jws.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return srv.URL, signAccess
}

func TestEnforcementBearerAuthedProjectsCookieless(t *testing.T) {
	reg := registry.New()
	registerEchoBearer(reg)
	idp.Register(reg)
	iss, signAccess := newBearerIdP(t, "c")
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  identity:
    claims: [email]
    groups: { source: isMemberOf, match: [ou=applicationRole], render: cn }
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app/cb }
---
kind: Route
metadata: { name: api }
spec: { match: /api/, type: service, handler: { type: echo-bearer } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok := signAccess(map[string]any{"email": "a@b.co", "isMemberOf": []any{"cn=admins,ou=applicationRole"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "user-123|admins|Bearer "+tok {
		t.Fatalf("body = %q (want id|groups|Authorization preserved)", rec.Body.String())
	}
}

func TestEnforcementBearerInvalid401(t *testing.T) {
	reg := registry.New()
	registerEchoBearer(reg)
	idp.Register(reg)
	iss, _ := newBearerIdP(t, "c")
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  identity: { claims: [email] }
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app/cb }
---
kind: Route
metadata: { name: api }
spec: { match: /api/, type: service, handler: { type: echo-bearer } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Fatalf("challenge = %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestEnforcementAppRouteIgnoresBearer(t *testing.T) {
	reg := registry.New()
	registerEchoBearer(reg)
	idp.Register(reg)
	iss, signAccess := newBearerIdP(t, "c")
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  identity: { claims: [email] }
  session: { key: "0123456789abcdef0123456789abcdef" }
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: `+iss+`, clientID: c, clientSecret: s, redirectURL: https://app/auth/callback }
---
kind: Route
metadata: { name: spa, labels: { x: y } }
spec: { match: /app/, type: app, handler: { type: echo-bearer }, access: { auth: required } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/app/", nil)
	req.Header.Set("Authorization", "Bearer "+signAccess(map[string]any{}))
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("app route must ignore Bearer and redirect, got %d", rec.Code)
	}
}

// appTestStore is an in-memory StateStore for the app e2e (registered under "memory").
type appTestStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *appTestStore) Get(_ context.Context, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return nil, session.ErrStateNotFound
	}
	return v, nil
}
func (s *appTestStore) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *appTestStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

func registerMemoryStore(reg *registry.Registry) {
	reg.Register(config.KindStateProvider, "memory", func(string, registry.RawConfig) (any, error) {
		return &appTestStore{m: map[string][]byte{}}, nil
	})
}

func TestBuildStatefulSessionReadThrough(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	registerMemoryStore(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "0123456789abcdef0123456789abcdef" }
  stateProvider: { type: memory }
---
kind: Route
metadata: { name: spa, labels: { x: y } }
spec: { match: /app/, type: app, handler: { type: echo-user }, access: { auth: required } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Session == nil {
		t.Fatal("stateful Session manager not built")
	}
	// mint a server-side session via the built manager, then read it back through the route
	wr := httptest.NewRecorder()
	mintReq := httptest.NewRequest("GET", "/", nil)
	mintReq.Header.Set("User-Agent", "UA")
	if err := a.Session.Issue(wr, mintReq, &idp.Identity{Subject: "u-1"},
		map[string]any{"email": "a@b.co"}, &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/app/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range wr.Result().Cookies() {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("authed stateful read status = %d, want 200", rec.Code)
	}
	// echo-user writes "X-User-Id|X-User-Groups"; no identity.groups is configured
	// here, so the groups half is empty ⇒ "u-1|".
	if rec.Body.String() != "u-1|" {
		t.Fatalf("projected body = %q, want \"u-1|\"", rec.Body.String())
	}
}

func TestBuildStatefulUnknownTypeFails(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "0123456789abcdef0123456789abcdef" }
  stateProvider: { type: nope }
---
kind: Route
metadata: { name: r }
spec: { match: /x/, type: app, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("Build must fail when stateProvider type is not registered")
	}
}

func TestBuildStateProviderRequiresSessionBlock(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	registerMemoryStore(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  stateProvider: { type: memory }
---
kind: Route
metadata: { name: r }
spec: { match: /x/, type: app, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("Build must fail when stateProvider is set without a session block")
	}
}

func TestBuildReverseProxyRouteServes(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		io.WriteString(w, "proxied")
	}))
	t.Cleanup(backend.Close)

	reg := registry.New()
	terminal.Register(reg) // includes reverse-proxy + api
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: api }
spec:
  match: /svc/
  type: service
  handler: { type: reverse-proxy, upstream: `+backend.URL+`, stripPrefix: /svc }
  access: { auth: public }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/svc/thing", nil))
	if rec.Code != 201 || rec.Body.String() != "proxied" {
		t.Fatalf("proxy route status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestBuildAPIRouteServes(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"v":1}`)
	}))
	t.Cleanup(backend.Close)

	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: dash }
spec:
  match: /dash/
  type: service
  handler:
    type: api
    backends:
      - { group: org, upstream: `+backend.URL+`, path: /org }
  access: { auth: public }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/dash/", nil))
	if rec.Code != 200 {
		t.Fatalf("api route status = %d", rec.Code)
	}
	if rec.Body.String() != `{"org":{"v":1}}`+"\n" { // json.Encoder appends a newline
		t.Fatalf("api route body = %q", rec.Body.String())
	}
}

func TestBuildServerSpanNamedByRoute(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))

	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: hc }
spec: { match: /health, handler: { type: health }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://h/health", nil))
	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no server span")
	}
	// otelhttp v0.69.0 renames the server span itself once r.Pattern is populated
	// by ServeMux dispatch (internal/semconv.HTTPServer.SpanName: "{METHOD}
	// {route}"). The span name and http.route attribute come entirely from
	// otelhttp's own server instrumentation — HOG does not set either.
	named := false
	for _, s := range spans {
		if s.Name() == "GET /health" {
			named = true
		}
	}
	if !named {
		t.Fatalf("no server span named %q; got %v", "GET /health", spanNames(spans))
	}
}

func TestBuildRecoverMarksSpanError(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	reg := registry.New()
	terminal.Register(reg)
	reg.Register(config.KindTerminalHandler, "boom", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }), nil
	})
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: b }
spec: { match: /boom, handler: { type: boom }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.Handler.ServeHTTP(rw, httptest.NewRequest("GET", "http://h/boom", nil))
	if rw.Code != 500 {
		t.Fatalf("status = %d, want 500", rw.Code)
	}
	spans := rec.Ended()
	errored := false
	for _, s := range spans {
		if s.Status().Code == codes.Error {
			errored = true
		}
	}
	if !errored {
		t.Fatal("panic did not mark a span error")
	}
}

// TestBuildAccessLogCarriesServiceAndConfiguredProperties verifies the
// service-decorated logger reaches the configured, trace-correlated access-log
// middleware wired into the skeleton: every access line carries the
// telemetry.Service.Name as `service`, plus the configured properties.
func TestBuildAccessLogCarriesServiceAndConfiguredProperties(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Telemetry
metadata: { name: t }
spec:
  service: { name: edge }
  accessLog: { properties: [method, status] }
---
kind: Route
metadata: { name: hc }
spec: { match: /health, handler: { type: health }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, logger)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://h/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	var line map[string]any
	found := false
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line not JSON: %v (%s)", err, l)
		}
		if m["msg"] == "access" {
			line = m
			found = true
		}
	}
	if !found {
		t.Fatalf("no access log line found in output: %s", buf.String())
	}
	if line["service"] != "edge" {
		t.Fatalf("service = %v, want edge", line["service"])
	}
	if line["method"] != "GET" {
		t.Fatalf("method = %v, want GET", line["method"])
	}
	if line["status"] != float64(200) {
		t.Fatalf("status = %v, want 200", line["status"])
	}
}

// TestBuildAccessLogAllowsStreamingFlush proves the wrapper chain built by the
// skeleton — otelhttp server handler → recover/request-id/access-log →
// terminal — stays flush-transparent, so httputil.ReverseProxy's
// FlushInterval:-1 SSE streaming (and websocket Hijack, which travels the same
// http.ResponseController/Unwrap path) reach the real ResponseWriter through
// the access-log wrapper. Without accessRecorder.Unwrap, ResponseController
// stops at the access-log layer and Flush fails with ErrNotSupported.
func TestBuildAccessLogAllowsStreamingFlush(t *testing.T) {
	reg := registry.New()
	terminal.Register(reg)
	reg.Register(config.KindTerminalHandler, "flushprobe", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := http.NewResponseController(w).Flush()
			if err != nil {
				io.WriteString(w, "flush-err: "+err.Error())
				return
			}
			io.WriteString(w, "flush-ok")
		}), nil
	})
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: s }
spec: { match: /s, handler: { type: flushprobe }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.Handler.ServeHTTP(rw, httptest.NewRequest("GET", "http://h/s", nil))
	if !strings.Contains(rw.Body.String(), "flush-ok") {
		t.Fatalf("streaming flush broken through the chain: %q", rw.Body.String())
	}
}

// TestBuildAccessLogRequestIDPresentForGeneratedIDs proves the request_id
// access-log property is populated even when the client sends no
// X-Request-Id: the request-id middleware (skeleton slot 2) generates one and
// sets it on the response header before access-log (slot 3) runs, and the
// access log reads it back via the recorder's response header.
func TestBuildAccessLogRequestIDPresentForGeneratedIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Telemetry
metadata: { name: t }
spec:
  service: { name: edge }
  accessLog: { properties: [request_id] }
---
kind: Route
metadata: { name: hc }
spec: { match: /health, handler: { type: health }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, logger)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rec := httptest.NewRecorder()
	// No X-Request-Id set by the client: request-id middleware must generate one.
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://h/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	var line map[string]any
	found := false
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line not JSON: %v (%s)", err, l)
		}
		if m["msg"] == "access" {
			line = m
			found = true
		}
	}
	if !found {
		t.Fatalf("no access log line found in output: %s", buf.String())
	}
	rid, _ := line["request_id"].(string)
	if rid == "" {
		t.Fatalf("request_id empty for generated id: %+v", line)
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	var out []string
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

// TestObservabilityEndToEnd proves the full telemetry stack works together
// through the FULL built chain (otelhttp server handler → skeleton →
// reverse-proxy terminal → instrumented backend transport):
//  1. an inbound W3C traceparent is CONTINUED, so the server span and the
//     child backend client span share the inbound trace id;
//  2. the backend receives a W3C traceparent on that same trace;
//  3. the access log line carries `service` and `trace_id`;
//  4. a `http.server.request.duration` metric datapoint carries `http.route`
//     set to the matched route pattern.
func TestObservabilityEndToEnd(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var backendTP string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendTP = r.Header.Get("traceparent")
		io.WriteString(w, "ok")
	}))
	t.Cleanup(backend.Close)

	reg := registry.New()
	terminal.Register(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Telemetry
metadata: { name: t }
spec: { service: { name: edge } }
---
kind: Route
metadata: { name: svc }
spec:
  match: /svc/
  type: service
  handler: { type: reverse-proxy, upstream: `+backend.URL+`, stripPrefix: /svc }
  access: { auth: public }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, logger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "http://hog/svc/thing", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	a.Handler.ServeHTTP(httptest.NewRecorder(), req)

	const traceID = "0af7651916cd43dd8448eb211c80319c"

	// (2) backend received a traceparent on the same trace
	if backendTP == "" || !strings.Contains(backendTP, traceID) {
		t.Fatalf("backend traceparent = %q (want trace %s)", backendTP, traceID)
	}
	// (1) server + client spans, both on the inbound trace
	spans := rec.Ended()
	if len(spans) < 2 {
		t.Fatalf("want server + client spans, got %d: %v", len(spans), spanNames(spans))
	}
	for _, s := range spans {
		if s.SpanContext().TraceID().String() != traceID {
			t.Fatalf("span %q on trace %s, want %s", s.Name(), s.SpanContext().TraceID(), traceID)
		}
	}
	// (3) access log carries service + trace_id
	if !bytes.Contains(buf.Bytes(), []byte(`"service":"edge"`)) ||
		!bytes.Contains(buf.Bytes(), []byte(`"trace_id":"`+traceID+`"`)) {
		t.Fatalf("access log missing service/trace_id: %s", buf.String())
	}
	// (4) server metric carries http.route
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if !metricHasRoute(rm, "/svc/") {
		t.Fatalf("no http.server.* metric with http.route=/svc/: %+v", rm)
	}
}

// metricHasRoute reports whether any metric datapoint carries http.route=route.
func metricHasRoute(rm metricdata.ResourceMetrics, route string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if hist, ok := m.Data.(metricdata.Histogram[float64]); ok {
				for _, dp := range hist.DataPoints {
					if v, present := dp.Attributes.Value("http.route"); present && v.AsString() == route {
						return true
					}
				}
			}
			if hist, ok := m.Data.(metricdata.Histogram[int64]); ok {
				for _, dp := range hist.DataPoints {
					if v, present := dp.Attributes.Value("http.route"); present && v.AsString() == route {
						return true
					}
				}
			}
		}
	}
	return false
}

func TestBuildAuthzAllowsSatisfyingPrincipal(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Policy
metadata: { name: admins }
spec: { require: { groups: [admins] } }
---
kind: Route
metadata: { name: a }
spec: { match: /admin/, type: service, handler: { type: echo-user }, access: { authorize: [admins] } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range mintSessionCookie(t, testKey) {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 { // minted principal IS in group admins ⇒ policy satisfied
		t.Fatalf("admin principal ⇒ 200, got %d", rec.Code)
	}
}

func TestBuildAuthzDeniesNonSatisfyingPrincipal(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Policy
metadata: { name: nobody }
spec: { require: { groups: [nobody] } }
---
kind: Route
metadata: { name: a }
spec: { match: /admin/, type: service, handler: { type: echo-user }, access: { authorize: [nobody] } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range mintSessionCookie(t, testKey) {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 403 { // minted principal NOT in group nobody ⇒ denied
		t.Fatalf("non-satisfying principal ⇒ 403, got %d", rec.Code)
	}
}

func TestBuildAuthzDanglingReferenceFailsBuild(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: a }
spec: { match: /x/, type: app, handler: { type: echo-user }, access: { authorize: [nope] } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("dangling policy reference must fail Build")
	}
}

func TestBuildAuthzBadRegoFailsBuild(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.rego"), []byte("package hog.authz\nthis is not rego"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Policy
metadata: { name: bad }
spec: { rego: { path: `+dir+` } }
---
kind: Route
metadata: { name: a }
spec: { match: /x/, type: app, handler: { type: echo-user }, access: { authorize: [bad] } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("bad rego must fail Build")
	}
}

func TestBuildNoPoliciesDefaultAllow(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: a }
spec: { match: /open/, type: app, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/open/", nil))
	if rec.Code != 200 { // no policies referenced ⇒ default-allow
		t.Fatalf("no-policy route ⇒ 200, got %d", rec.Code)
	}
}

// TestBuildAuthzPolicyViaRouteGroupEnforced proves a policy applied through a
// matching RouteGroup's `policies` — not the route's own `policies` — is part
// of the route's effective policy set and is enforced.
func TestBuildAuthzPolicyViaRouteGroupEnforced(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Policy
metadata: { name: restricted }
spec: { require: { groups: [nobody] } }
---
kind: RouteGroup
metadata: { name: locked }
spec:
  selector: { matchLabels: { tier: locked } }
  access: { authorize: [restricted] }
---
kind: Route
metadata: { name: a, labels: { tier: locked } }
spec: { match: /locked/, type: service, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/locked/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range mintSessionCookie(t, testKey) {
		req.AddCookie(c)
	}
	a.Handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("group-applied policy (require nobody) ⇒ 403, got %d", rec.Code)
	}
}

// TestBuildAuthzGatePerRouteIsolation proves the per-route authz gate built
// for a denying route does not leak onto an adjacent route built in the same
// pass: /locked/ (labeled, gets the RouteGroup's denying policy) must 403,
// while /open/ (unlabeled, no effective policy) must still 200.
func TestBuildAuthzGatePerRouteIsolation(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec:
  session: { key: "`+testKey+`" }
---
kind: Policy
metadata: { name: restricted }
spec: { require: { groups: [nobody] } }
---
kind: RouteGroup
metadata: { name: locked }
spec:
  selector: { matchLabels: { tier: locked } }
  access: { authorize: [restricted] }
---
kind: Route
metadata: { name: a, labels: { tier: locked } }
spec: { match: /locked/, type: service, handler: { type: echo-user } }
---
kind: Route
metadata: { name: b }
spec: { match: /open/, type: app, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	do := func(path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("User-Agent", "UA")
		for _, c := range mintSessionCookie(t, testKey) {
			req.AddCookie(c)
		}
		a.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("/locked/"); c != 403 {
		t.Fatalf("/locked/ (denying policy) ⇒ 403, got %d", c)
	}
	if c := do("/open/"); c != 200 {
		t.Fatalf("/open/ (no policy) ⇒ 200 (authz gate must not leak), got %d", c)
	}
}

func TestBuildForwardedStripsUntrustedProto(t *testing.T) {
	reg := registry.New()
	reg.Register(config.KindTerminalHandler, "echo-proto", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Header.Get("X-Forwarded-Proto"))
		}), nil
	})
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-proto }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-Proto", "http")
	a.Handler.ServeHTTP(rec, req)
	if rec.Body.String() != "" {
		t.Fatalf("untrusted X-Forwarded-Proto reached the handler: %q", rec.Body.String())
	}
}

// TestBuildForwardedStripsUntrustedRealIP confirms the outermost forwarded
// wrap (applied before otelhttp, ahead of routing) strips a spoofed
// X-Real-IP from an untrusted peer end-to-end through a.Handler.
func TestBuildForwardedStripsUntrustedRealIP(t *testing.T) {
	reg := registry.New()
	reg.Register(config.KindTerminalHandler, "echo-real-ip", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Header.Get("X-Real-IP"))
		}), nil
	})
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-real-ip }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Real-IP", "9.9.9.9")
	a.Handler.ServeHTTP(rec, req)
	if rec.Body.String() != "" {
		t.Fatalf("untrusted X-Real-IP reached the handler: %q", rec.Body.String())
	}
}

func TestBuildForwardedTrustsWildcard(t *testing.T) {
	reg := registry.New()
	reg.Register(config.KindTerminalHandler, "echo-proto2", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Header.Get("X-Forwarded-Proto"))
		}), nil
	})
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: { trustedProxies: ["*"] }
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-proto2 }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	a.Handler.ServeHTTP(rec, req)
	if rec.Body.String() != "http" {
		t.Fatalf("trusted X-Forwarded-Proto should be preserved, got %q", rec.Body.String())
	}
}

// TestBuildFailsFastOnBadTrustedProxies proves an invalid trustedProxies
// entry (not an IP, CIDR, or "*") fails Build rather than silently degrading
// the forwarded stage at request time.
func TestBuildFailsFastOnBadTrustedProxies(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: { trustedProxies: ["bogus"] }
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-user }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, reg, nil); err == nil {
		t.Fatal("Build must fail fast on an invalid trustedProxies entry")
	}
}

// TestBuildSecurityDefaults proves the security stage is wired end-to-end
// through Build: the configurable security headers are set on every response,
// and a cross-origin unsafe request is rejected by CSRF protection.
func TestBuildSecurityDefaults(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-user }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header: %v", rec.Header())
	}
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/p", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	a.Handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", rec2.Code)
	}
}

// TestBuildSecurityHeadersGatewayWide proves security is wired as the
// gateway-wide outermost wrap (forwarded(security(otel(mux)))), not a
// per-route skeleton slot: a request to a path with NO matching route still
// carries the security headers, since it never reaches a route's skeleton.
// This is what makes the raw auth endpoints (login/logout/callback/info,
// mounted directly on the mux) covered too.
func TestBuildSecurityHeadersGatewayWide(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Route
metadata: { name: p }
spec: { match: /p, type: app, handler: { type: echo-user }, access: { auth: public } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("missing nosniff header on unmatched path: %v", rec.Header())
	}
}

func TestBuildAuthzEndToEnd(t *testing.T) {
	reg := registry.New()
	registerEcho(reg)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.rego"), []byte(`package hog.authz
import rego.v1
deny contains msg if { input.request.method == "DELETE"; msg := "no deletes" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(mustDecode(t, `
kind: Gateway
metadata: { name: hog }
spec: {}
---
kind: Policy
metadata: { name: no-delete }
spec: { rego: { path: `+dir+` } }
---
kind: Route
metadata: { name: pub }
spec: { match: /pub/, type: app, handler: { type: echo-user }, access: { authorize: [no-delete] } }
---
kind: Route
metadata: { name: open }
spec: { match: /open/, type: app, handler: { type: echo-user } }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		a.Handler.ServeHTTP(rec, httptest.NewRequest(method, "http://h"+path, nil))
		return rec.Code
	}
	if c := do("GET", "/pub/"); c != 200 {
		t.Fatalf("GET /pub/ ⇒ %d, want 200 (rego allows GET)", c)
	}
	if c := do("DELETE", "/pub/"); c != 403 {
		t.Fatalf("DELETE /pub/ ⇒ %d, want 403 (rego deny)", c)
	}
	if c := do("DELETE", "/open/"); c != 200 {
		t.Fatalf("DELETE /open/ (no policies) ⇒ %d, want 200 (default-allow)", c)
	}
}
