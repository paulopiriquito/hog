package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"
)

func node(t *testing.T, y string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return *n.Content[0]
	}
	return n
}

func TestDefaultsSetSecurityHeaders(t *testing.T) {
	cfg, err := Parse(yaml.Node{}) // empty ⇒ defaults
	if err != nil {
		t.Fatal(err)
	}
	mw, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for h, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS should default on")
	}
	if rec.Header().Get("Referrer-Policy") == "" {
		t.Error("Referrer-Policy should default on")
	}
}

func TestHeaderDisableViaEmpty(t *testing.T) {
	cfg, _ := Parse(node(t, `headers: { frameOptions: "", hsts: { enabled: false } }`))
	mw, _ := Build(cfg)
	rec := httptest.NewRecorder()
	mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Error("empty frameOptions should disable the header")
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("hsts.enabled=false should disable HSTS")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("unset contentTypeOptions should still default to nosniff")
	}
}

func TestCSRFBlocksCrossOriginUnsafe(t *testing.T) {
	cfg, _ := Parse(yaml.Node{})
	mw, _ := Build(cfg)
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options on 403 = %q, want DENY (headers must be set outermost, even on CSRF rejections)", got)
	}
	rec2 := httptest.NewRecorder() // no Sec-Fetch-Site ⇒ non-browser ⇒ allowed
	h.ServeHTTP(rec2, httptest.NewRequest("POST", "/", nil))
	if rec2.Code != 200 {
		t.Fatalf("non-browser POST = %d, want 200", rec2.Code)
	}
}

func TestCSRFDisabled(t *testing.T) {
	cfg, _ := Parse(node(t, `csrf: { enabled: false }`))
	mw, _ := Build(cfg)
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("csrf disabled: cross-site POST = %d, want 200", rec.Code)
	}
}

func TestBadTrustedOriginFailsBuild(t *testing.T) {
	cfg, _ := Parse(node(t, `csrf: { trustedOrigins: ["not a url"] }`))
	if _, err := Build(cfg); err == nil {
		t.Fatal("want error for an invalid trustedOrigin")
	}
}

func TestTrustedOriginAllowsCrossSite(t *testing.T) {
	cfg, err := Parse(node(t, `csrf: { trustedOrigins: ["https://app.example.com"] }`))
	if err != nil {
		t.Fatal(err)
	}
	mw, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("trusted-origin cross-site POST = %d, want 200", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/", nil)
	req2.Header.Set("Origin", "https://evil.example.com")
	req2.Header.Set("Sec-Fetch-Site", "cross-site")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("untrusted-origin cross-site POST = %d, want 403", rec2.Code)
	}
}

// TestBadBypassPatternFailsBuild proves a malformed bypassPatterns entry
// fails Build with a clean error instead of panicking. An unclosed wildcard
// segment ("/x/{") is syntactically invalid ServeMux pattern syntax and
// panics inside net/http.CrossOriginProtection.AddInsecureBypassPattern;
// addBypass must convert that into a returned error.
func TestBadBypassPatternFailsBuild(t *testing.T) {
	cfg, err := Parse(node(t, `csrf: { bypassPatterns: ["/x/{"] }`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg); err == nil {
		t.Fatal("want error for a malformed bypassPattern, got nil (Build must not panic)")
	}
}
