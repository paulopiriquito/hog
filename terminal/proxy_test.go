package terminal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

// cfgNode turns a YAML handler config string into a registry.RawConfig.
func cfgNode(t *testing.T, s string) registry.RawConfig {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return registry.RawConfig{Node: *n.Content[0]}
	}
	return registry.RawConfig{Node: n}
}

// buildHandler registers all terminals and builds one by type+config.
func buildHandler(t *testing.T, typ, cfg string) http.Handler {
	t.Helper()
	reg := registry.New()
	Register(reg)
	m, err := reg.Build(config.KindTerminalHandler, typ, cfgNode(t, cfg))
	if err != nil {
		t.Fatalf("build %s: %v", typ, err)
	}
	h, ok := m.(http.Handler)
	if !ok {
		t.Fatalf("%s is not an http.Handler", typ)
	}
	return h
}

type recordingBackend struct {
	srv  *httptest.Server
	last *http.Request
	body string
}

func newRecordingBackend(t *testing.T, status int, body string) *recordingBackend {
	t.Helper()
	b := &recordingBackend{body: body}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.last = r.Clone(r.Context())
		w.Header().Set("X-Backend", "hit")
		w.WriteHeader(status)
		io.WriteString(w, b.body)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func TestProxyHappyPathAndStripPrefix(t *testing.T) {
	be := newRecordingBackend(t, 200, "hello")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\nstripPrefix: /api/orders\n")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://hog.example/api/orders/123?q=1", nil)
	req.Header.Set("X-User-Id", "u-1")
	req.Header.Set("Cookie", "hog_session=abc")
	h.ServeHTTP(rec, req)

	if rec.Code != 200 || rec.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Backend") != "hit" {
		t.Fatal("backend response headers not proxied")
	}
	if be.last.URL.Path != "/123" {
		t.Fatalf("stripPrefix path = %q, want /123", be.last.URL.Path)
	}
	if be.last.Header.Get("X-User-Id") != "u-1" {
		t.Fatal("X-User-Id not forwarded to backend")
	}
	if be.last.Header.Get("Cookie") != "" {
		t.Fatalf("cookie reached backend: %q", be.last.Header.Get("Cookie"))
	}
}

func TestProxyForwardsAccessTokenWhenOptedIn(t *testing.T) {
	be := newRecordingBackend(t, 204, "")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\nforwardAccessToken: true\n")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://hog.example/x", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{Subject: "u", AccessToken: "AT-9"}))
	h.ServeHTTP(rec, req)

	if be.last.Header.Get("Authorization") != "Bearer AT-9" {
		t.Fatalf("backend Authorization = %q", be.last.Header.Get("Authorization"))
	}
}

func TestProxyDownBackend502(t *testing.T) {
	be := newRecordingBackend(t, 200, "x")
	url := be.srv.URL
	be.srv.Close() // now refused
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+url+"\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("down backend status = %d, want 502", rec.Code)
	}
}

func TestProxySlowBackend504(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	t.Cleanup(slow.Close)
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+slow.URL+"\ntimeout: 20ms\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("slow backend status = %d, want 504", rec.Code)
	}
}

func TestProxyBuildErrors(t *testing.T) {
	reg := registry.New()
	Register(reg)
	if _, err := reg.Build(config.KindTerminalHandler, "reverse-proxy", cfgNode(t, "type: reverse-proxy\n")); err == nil {
		t.Fatal("missing upstream must error")
	}
	if _, err := reg.Build(config.KindTerminalHandler, "reverse-proxy", cfgNode(t, "type: reverse-proxy\nupstream: not-a-url\n")); err == nil {
		t.Fatal("invalid upstream must error")
	}
	if _, err := reg.Build(config.KindTerminalHandler, "reverse-proxy", cfgNode(t, "type: reverse-proxy\nupstream: http://x:1\ntimeout: not-a-duration\n")); err == nil {
		t.Fatal("invalid timeout must error")
	}
}

func TestProxyPreserveHost(t *testing.T) {
	be := newRecordingBackend(t, 200, "ok")

	// default: backend sees the upstream host
	hDefault := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\n")
	rec := httptest.NewRecorder()
	hDefault.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if be.last.Host == "hog.example" {
		t.Fatalf("default must NOT preserve host, backend saw %q", be.last.Host)
	}

	// preserveHost: backend sees the inbound host
	hPreserve := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\npreserveHost: true\n")
	rec2 := httptest.NewRecorder()
	hPreserve.ServeHTTP(rec2, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if be.last.Host != "hog.example" {
		t.Fatalf("preserveHost must keep inbound host, backend saw %q", be.last.Host)
	}
}

func TestProxyPassesThroughBackend5xx(t *testing.T) {
	be := newRecordingBackend(t, 503, "down")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != 503 || rec.Body.String() != "down" {
		t.Fatalf("backend 5xx must pass through, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestProxyForwardCookiesReachBackend(t *testing.T) {
	be := newRecordingBackend(t, 200, "")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\nforwardCookies: true\n")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://hog.example/x", nil)
	req.Header.Set("Cookie", "app=1")
	h.ServeHTTP(rec, req)
	if be.last.Header.Get("Cookie") != "app=1" {
		t.Fatalf("forwardCookies must pass the cookie, backend saw %q", be.last.Header.Get("Cookie"))
	}
}

func TestProxyStripPrefixWholePathYieldsRoot(t *testing.T) {
	be := newRecordingBackend(t, 200, "")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\nstripPrefix: /api\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/api", nil))
	if be.last.URL.Path != "/" {
		t.Fatalf("stripping the whole path (no upstream base path) must yield /, got %q", be.last.URL.Path)
	}
}

// TestProxyStripsClientAuthorizationWhenNotForwarding verifies that a
// client-supplied Authorization header is never leaked to the backend when
// forwardAccessToken is not set — even when the inbound request carries one.
func TestProxyStripsClientAuthorizationWhenNotForwarding(t *testing.T) {
	be := newRecordingBackend(t, 200, "ok")
	h := buildHandler(t, "reverse-proxy", "type: reverse-proxy\nupstream: "+be.srv.URL+"\n")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://hog.example/x", nil)
	req.Header.Set("Authorization", "Bearer x")
	h.ServeHTTP(rec, req)
	if got := be.last.Header.Get("Authorization"); got != "" {
		t.Fatalf("backend must not receive client Authorization when forwardAccessToken=false, got %q", got)
	}
}
