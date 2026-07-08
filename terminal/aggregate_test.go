package terminal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/session"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestAPIMergesUnderGroups(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"name":"Acme"}`)
	}))
	t.Cleanup(a.Close)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[1,2,3]`)
	}))
	t.Cleanup(b.Close)

	h := buildHandler(t, "api", `
type: api
backends:
  - { group: org,     upstream: `+a.URL+`, path: /org }
  - { group: members, upstream: `+b.URL+`, path: /members }
`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/api/dash", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("merged not JSON: %v (%s)", err, rec.Body.String())
	}
	if string(got["org"]) != `{"name":"Acme"}` || string(got["members"]) != `[1,2,3]` {
		t.Fatalf("merged = %s", rec.Body.String())
	}
}

func TestAPISubstitutesParamAndForwardsQuery(t *testing.T) {
	var lastPath, lastQuery string
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath, lastQuery = r.URL.Path, r.URL.RawQuery
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(be.Close)

	h := buildHandler(t, "api", "type: api\nbackends:\n  - group: org\n    upstream: "+be.URL+"\n    path: /orgs/{orgID}/members\n    forwardQuery: true\n")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://hog.example/api/orgs/o-1/dash?since=2024", nil)
	req.SetPathValue("orgID", "o-1")
	h.ServeHTTP(rec, req)
	if lastPath != "/orgs/o-1/members" {
		t.Fatalf("backend path = %q", lastPath)
	}
	if lastQuery != "since=2024" {
		t.Fatalf("backend query = %q", lastQuery)
	}
}

func TestAPIRequiredBackendDown502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	url := up.URL
	up.Close() // refused
	h := buildHandler(t, "api", "type: api\nbackends:\n  - { group: org, upstream: "+url+", path: /org }\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("required down status = %d, want 502", rec.Code)
	}
}

func TestAPIRequiredBackendTimeout504(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(slow.Close)
	h := buildHandler(t, "api", "type: api\ntimeout: 20ms\nbackends:\n  - { group: org, upstream: "+slow.URL+", path: /org }\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("required timeout status = %d, want 504", rec.Code)
	}
}

func TestAPIOptionalBackendDownIsPartial(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	t.Cleanup(good.Close)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := bad.URL
	bad.Close()

	h := buildHandler(t, "api", `
type: api
backends:
  - { group: org,     upstream: `+good.URL+`, path: /org }
  - { group: billing, upstream: `+badURL+`,   path: /billing, required: false }
`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != 200 {
		t.Fatalf("optional down status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Hog-Partial") != "billing" {
		t.Fatalf("X-Hog-Partial = %q, want billing", rec.Header().Get("X-Hog-Partial"))
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if _, present := got["billing"]; present {
		t.Fatal("failed optional group must be omitted")
	}
	if string(got["org"]) != `{"ok":true}` {
		t.Fatalf("org group = %s", got["org"])
	}
}

func TestAPINonJSONRequired502(t *testing.T) {
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "not json") }))
	t.Cleanup(be.Close)
	h := buildHandler(t, "api", "type: api\nbackends:\n  - { group: org, upstream: "+be.URL+", path: /org }\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("non-JSON required status = %d, want 502", rec.Code)
	}
}

func TestAPIBuildErrors(t *testing.T) {
	reg := registry.New()
	Register(reg)
	cases := []string{
		"type: api\n", // no backends
		"type: api\nbackends:\n  - { group: g, upstream: http://x, path: /a }\n  - { group: g, upstream: http://y, path: /b }\n", // dup group
		"type: api\nbackends:\n  - { group: g, upstream: nope, path: /a }\n",                                                     // bad upstream
		"type: api\nbackends:\n  - { group: g, upstream: http://x }\n",                                                           // empty path
		"type: api\nbackends:\n  - { group: g, upstream: http://x, path: /a, method: \"GE T\" }\n",                               // invalid method token
	}
	for i, c := range cases {
		if _, err := reg.Build(config.KindTerminalHandler, "api", cfgNode(t, c)); err == nil {
			t.Fatalf("case %d: want build error", i)
		}
	}
}

func TestAPIParamTraversalRejected(t *testing.T) {
	var sawPath string
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(be.Close)

	cases := []string{"../../admin", "..", "a/b", "."}
	for _, bad := range cases {
		h := buildHandler(t, "api", "type: api\nbackends:\n  - group: org\n    upstream: "+be.URL+"\n    path: /orgs/{seg}/members\n")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "http://hog.example/x", nil)
		req.SetPathValue("seg", bad)
		sawPath = ""
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("value %q: status = %d, want 502 (fail-closed)", bad, rec.Code)
		}
		if sawPath != "" {
			t.Fatalf("value %q: backend was reached at %q (must be rejected before the request)", bad, sawPath)
		}
	}

	// Empty/unmatched {param}: the router never set orgID so r.PathValue returns "".
	// safePathValue must reject it fail-closed — the backend must not be reached.
	t.Run("empty_unmatched_param", func(t *testing.T) {
		h := buildHandler(t, "api", "type: api\nbackends:\n  - group: org\n    upstream: "+be.URL+"\n    path: /orgs/{orgID}/members\n")
		rec := httptest.NewRecorder()
		// Deliberately do NOT call req.SetPathValue("orgID", ...) — r.PathValue("orgID") == "".
		req := httptest.NewRequest("GET", "http://hog.example/x", nil)
		sawPath = ""
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("empty param: status = %d, want 502 (fail-closed)", rec.Code)
		}
		if sawPath != "" {
			t.Fatalf("empty param: backend was reached at %q (must be rejected before the request)", sawPath)
		}
	})
}

func TestAPIForwardAccessTokenPerBackend(t *testing.T) {
	var withAuth, withoutAuth string
	yes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(yes.Close)
	no := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withoutAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(no.Close)

	h := buildHandler(t, "api", "type: api\nbackends:\n"+
		"  - group: a\n    upstream: "+yes.URL+"\n    path: /a\n    forwardAccessToken: true\n"+
		"  - group: b\n    upstream: "+no.URL+"\n    path: /b\n")

	// Build a request whose context carries a Principal with an access token —
	// mirrors the exact construction from terminal/proxy_test.go.
	ctx := session.WithPrincipal(httptest.NewRequest("GET", "http://hog.example/x", nil).Context(),
		&session.Principal{Subject: "u-1", AccessToken: "tok-123"})
	req := httptest.NewRequest("GET", "http://hog.example/x", nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if withAuth != "Bearer tok-123" {
		t.Fatalf("opted-in backend Authorization = %q, want Bearer tok-123", withAuth)
	}
	if withoutAuth != "" {
		t.Fatalf("non-opted backend leaked Authorization = %q, want empty", withoutAuth)
	}
}

func TestAPIBackendCallEmitsClientSpanAndPropagates(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	var gotTraceparent string
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(be.Close)

	h := buildHandler(t, "api", "type: api\nbackends:\n  - { group: org, upstream: "+be.URL+", path: /org }\n")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec2.Code != 200 {
		t.Fatalf("status = %d", rec2.Code)
	}

	if gotTraceparent == "" {
		t.Fatal("backend did not receive a traceparent")
	}
	var found bool
	for _, span := range rec.Ended() {
		for _, a := range span.Attributes() {
			if string(a.Key) == "hog.backend" && a.Value.AsString() == "org" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no client span with hog.backend=org recorded; spans: %v", rec.Ended())
	}
}

// TestAPIConcurrentBackendsDoNotBleedSpanAttributes fans out to several
// backends concurrently (api aggregation calls every backend in its own
// goroutine, sharing one InstrumentedTransport) and verifies each backend's
// client span carries ITS OWN hog.backend group — i.e. the per-request
// context/span used to tag hog.backend never bleeds across goroutines onto a
// different backend's span. The span's own server.address/server.port
// (independent of hog.backend) is used to know which backend a span actually
// hit, so this is a real cross-check and not circular. Run with -race.
func TestAPIConcurrentBackendsDoNotBleedSpanAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	groups := []string{"a", "b", "c", "d", "e", "f"}
	servers := make(map[string]*httptest.Server, len(groups))
	hostToGroup := make(map[string]string, len(groups))
	for _, g := range groups {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{}`)
		}))
		t.Cleanup(srv.Close)
		servers[g] = srv
		hostToGroup[srv.Listener.Addr().String()] = g
	}

	var cfg strings.Builder
	cfg.WriteString("type: api\nbackends:\n")
	for _, g := range groups {
		fmt.Fprintf(&cfg, "  - { group: %s, upstream: %s, path: /%s }\n", g, servers[g].URL, g)
	}

	h := buildHandler(t, "api", cfg.String())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "http://hog.example/x", nil))
	if rec2.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	seenGroups := make(map[string]bool, len(groups))
	for _, span := range rec.Ended() {
		var host string
		var port int64
		var backend string
		var hasBackend bool
		for _, a := range span.Attributes() {
			switch string(a.Key) {
			case "server.address":
				host = a.Value.AsString()
			case "server.port":
				port = a.Value.AsInt64()
			case "hog.backend":
				backend = a.Value.AsString()
				hasBackend = true
			}
		}
		if !hasBackend {
			continue // not a backend client span (e.g. an internal/server span)
		}
		wantGroup, known := hostToGroup[host+":"+strconv.FormatInt(port, 10)]
		if !known {
			t.Fatalf("span hit unknown host %s:%d with hog.backend=%q", host, port, backend)
		}
		if backend != wantGroup {
			t.Fatalf("span to %s:%d has hog.backend=%q, want %q (cross-goroutine bleed)", host, port, backend, wantGroup)
		}
		seenGroups[backend] = true
	}
	if len(seenGroups) != len(groups) {
		t.Fatalf("saw spans for groups %v, want all of %v", seenGroups, groups)
	}
}
