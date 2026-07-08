package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
  policy: { auth: required }
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
  policy: { auth: required }
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
spec: { match: /app/, type: app, handler: { type: echo-bearer }, policy: { auth: required } }
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
spec: { match: /app/, type: app, handler: { type: echo-user }, policy: { auth: required } }
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
  policy: { auth: public }
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
  policy: { auth: public }
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
