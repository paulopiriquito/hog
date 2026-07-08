package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/session"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func policyResource(t *testing.T, spec string) config.Resource {
	t.Helper()
	rs, err := config.DecodeAll([]byte("kind: Policy\nmetadata: { name: p }\nspec:\n" + spec))
	if err != nil || len(rs) != 1 {
		t.Fatalf("decode: %v (n=%d)", err, len(rs))
	}
	return rs[0]
}

func serve(t *testing.T, g interface {
	Wrap(http.Handler) http.Handler
}, p *session.Principal) int {
	t.Helper()
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "http://h/admin", nil)
	if p != nil {
		req = req.WithContext(session.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestGateDefaultAllowNoPolicies(t *testing.T) {
	if code := serve(t, Gate(nil, "r", nil, nil), nil); code != 200 {
		t.Fatalf("no policies ⇒ allow, got %d", code)
	}
}

func TestGateRequireDenyAndAllow(t *testing.T) {
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [admins] }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Gate([]*Policy{pols["p"]}, "r", nil, nil)
	if code := serve(t, g, &session.Principal{Subject: "u", Groups: []string{"users"}}); code != 403 {
		t.Fatalf("missing group ⇒ 403, got %d", code)
	}
	if code := serve(t, g, &session.Principal{Subject: "u", Groups: []string{"admins"}}); code != 200 {
		t.Fatalf("has group ⇒ 200, got %d", code)
	}
}

func TestGate403HasNoPolicyDetail(t *testing.T) {
	pols, _ := Compile(context.Background(), []config.Resource{policyResource(t, "  require: { groups: [x] }\n")})
	h := Gate([]*Policy{pols["p"]}, "r", nil, nil).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://h/x", nil))
	if rec.Code != 403 {
		t.Fatalf("code = %d", rec.Code)
	}
	if b := rec.Body.String(); containsAny(b, "x", "group", "require") {
		t.Fatalf("403 body leaked policy detail: %q", b)
	}
}

func TestCompileEmptyPolicyErrors(t *testing.T) {
	if _, err := Compile(context.Background(), []config.Resource{policyResource(t, "  {}\n")}); err == nil {
		t.Fatal("empty policy (no require/rego) ⇒ error")
	}
}

func TestGateConcurrentEval(t *testing.T) {
	dir := writeRego(t, denyDelete) // from engine_test.go (same package)
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  rego: { path: "+dir+" }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Gate([]*Policy{pols["p"]}, "r", nil, nil)
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://h/x", nil)) // GET ⇒ allow
			if rec.Code != 200 {
				t.Errorf("concurrent GET ⇒ %d, want 200", rec.Code)
			}
		}()
	}
	wg.Wait()
}

// TestGateAnonymousRegoDenyFiresFailClosed is the C1 regression through the
// full gate: before the buildInput fix, a nil principal omitted the
// "groups" key entirely, so `not "admins" in input.groups` referenced an
// undefined path and the deny never fired — an anonymous DELETE was
// silently allowed. With groups present-but-empty, the deny fires.
func TestGateAnonymousRegoDenyFiresFailClosed(t *testing.T) {
	dir := writeRego(t, denyDelete)
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  rego: { path: "+dir+" }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Gate([]*Policy{pols["p"]}, "r", nil, nil)

	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("DELETE", "http://h/x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous DELETE through rego deny ⇒ 403, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://h/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous GET (rego only denies DELETE) ⇒ 200, got %d", rec.Code)
	}
}

// TestGateDenyOverridesAcrossPolicies: policy A's require is satisfied by the
// principal, policy B's is not — the effective set must deny (403) even
// though A alone would allow. Every policy in the set must be satisfied.
func TestGateDenyOverridesAcrossPolicies(t *testing.T) {
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [users] }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	polA := pols["p"]

	rs, err := config.DecodeAll([]byte("kind: Policy\nmetadata: { name: b }\nspec:\n  require: { groups: [admins] }\n"))
	if err != nil || len(rs) != 1 {
		t.Fatalf("decode: %v (n=%d)", err, len(rs))
	}
	polsB, err := Compile(context.Background(), rs)
	if err != nil {
		t.Fatal(err)
	}
	polB := polsB["b"]

	g := Gate([]*Policy{polA, polB}, "r", nil, nil)
	code := serve(t, g, &session.Principal{Subject: "u", Groups: []string{"users"}})
	if code != http.StatusForbidden {
		t.Fatalf("policy B unsatisfied ⇒ 403 despite policy A allowing, got %d", code)
	}
}

// TestGateRegoDenyThroughHTTP exercises a rego-only policy end to end through
// Gate(...).Wrap for a DELETE by a non-admin — must be denied.
func TestGateRegoDenyThroughHTTP(t *testing.T) {
	dir := writeRego(t, denyDelete)
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  rego: { path: "+dir+" }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Gate([]*Policy{pols["p"]}, "r", nil, nil)
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("DELETE", "http://h/x", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{Subject: "u", Groups: []string{"users"}}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin DELETE through rego-deny policy ⇒ 403, got %d", rec.Code)
	}
}

// TestGateDenyRecordsSpanEvent asserts a denied request records an
// authz.deny span event carrying an authz.policy attribute, so operators can
// see denials in traces (not just logs).
func TestGateDenyRecordsSpanEvent(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prevTP) })

	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [admins] }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Gate([]*Policy{pols["p"]}, "r", nil, nil)
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	ctx, span := otel.Tracer("authz_test").Start(context.Background(), "test-request-span")
	req := httptest.NewRequest("GET", "http://h/admin", nil).WithContext(ctx)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{Subject: "u", Groups: []string{"users"}}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	span.End()

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(ended))
	}
	events := ended[0].Events()
	var denyEvent *string
	for i := range events {
		if events[i].Name == "authz.deny" {
			name := events[i].Name
			denyEvent = &name
			foundPolicy := false
			for _, a := range events[i].Attributes {
				if string(a.Key) == "authz.policy" && a.Value.AsString() == "p" {
					foundPolicy = true
				}
			}
			if !foundPolicy {
				t.Fatalf("authz.deny event missing authz.policy=p attribute: %v", events[i].Attributes)
			}
		}
	}
	if denyEvent == nil {
		t.Fatalf("expected an authz.deny span event, got events: %v", events)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
