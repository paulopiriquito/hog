package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/session"
)

// principalProbe is a terminal that records whether a principal is in ctx.
func principalProbe(seen *bool, subj *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := session.FromContext(r.Context()); ok {
			*seen = true
			*subj = p.Subject
		}
	})
}

func sessionWithCookie(t *testing.T) (session.Manager, []*http.Cookie) {
	t.Helper()
	m, err := session.NewManager(session.Config{
		CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour,
		FingerprintHeaders: []string{"User-Agent"}, PassportClaims: []string{"email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	s := m.New(&idp.Identity{Subject: "u-1", Claims: map[string]any{"email": "a@b.co"}},
		nil, &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}, rq)
	_ = m.Write(wr, rq, s)
	return m, wr.Result().Cookies()
}

func TestSessionGateAuthenticated(t *testing.T) {
	m, cookies := sessionWithCookie(t)
	var seen bool
	var subj string
	h := chain.Compose(principalProbe(&seen, &subj), SessionGate(m))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	h.ServeHTTP(rec, req)
	if !seen || subj != "u-1" {
		t.Fatalf("principal not resolved: seen=%v subj=%q", seen, subj)
	}
}

func TestSessionGateUnauthenticatedAndNil(t *testing.T) {
	m, _ := sessionWithCookie(t)
	// no cookie ⇒ no principal
	var seen bool
	var subj string
	chain.Compose(principalProbe(&seen, &subj), SessionGate(m)).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen {
		t.Fatal("no cookie must not yield a principal")
	}
	// nil manager ⇒ pass-through, no principal, no panic
	seen = false
	chain.Compose(principalProbe(&seen, &subj), SessionGate(nil)).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen {
		t.Fatal("nil manager must pass through")
	}
}

func passProbe() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
}

func withPrincipal(r *http.Request) *http.Request {
	return r.WithContext(session.WithPrincipal(r.Context(), &session.Principal{Subject: "u-1"}))
}

func TestAuthGateAppRedirects(t *testing.T) {
	h := chain.Compose(passProbe(), AuthGate(true, true, "/auth/login"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/app/x?q=1", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/auth/login?return_to=%2Fapp%2Fx%3Fq%3D1" {
		t.Fatalf("location = %q", loc)
	}
}

func TestAuthGateServiceReturns401(t *testing.T) {
	h := chain.Compose(passProbe(), AuthGate(true, false, "/auth/login"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthGatePassesWhenAuthedOrNotRequired(t *testing.T) {
	// required + authed ⇒ pass
	h := chain.Compose(passProbe(), AuthGate(true, true, "/auth/login"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withPrincipal(httptest.NewRequest("GET", "/app/x", nil)))
	if rec.Code != 200 {
		t.Fatalf("authed status = %d, want 200", rec.Code)
	}
	// not required ⇒ pass even unauth
	rec2 := httptest.NewRecorder()
	chain.Compose(passProbe(), AuthGate(false, true, "/auth/login")).
		ServeHTTP(rec2, httptest.NewRequest("GET", "/app/x", nil))
	if rec2.Code != 200 {
		t.Fatalf("public status = %d, want 200", rec2.Code)
	}
}

func TestAuthGateAppRedirectPreservesLoginQuery(t *testing.T) {
	h := chain.Compose(passProbe(), AuthGate(true, true, "/auth/login?prompt=login"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/app/x", nil))
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location not a valid URL: %q (%v)", loc, err)
	}
	if u.Path != "/auth/login" || u.Query().Get("prompt") != "login" || u.Query().Get("return_to") != "/app/x" {
		t.Fatalf("redirect dropped/garbled query: %q", loc)
	}
}

func TestSessionGateTamperedCookieNoPrincipal(t *testing.T) {
	m, cookies := sessionWithCookie(t)
	var seen bool
	var subj string
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "UA")
	for _, c := range cookies {
		tampered := *c
		tampered.Value = c.Value + "x" // corrupt the sealed value
		req.AddCookie(&tampered)
	}
	chain.Compose(principalProbe(&seen, &subj), SessionGate(m)).ServeHTTP(httptest.NewRecorder(), req)
	if seen {
		t.Fatal("tampered cookie must not resolve a principal")
	}
}
