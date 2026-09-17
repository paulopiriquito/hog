package auth

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/v2/chain"
	"github.com/paulopiriquito/hog/v2/session"
)

// assertionProbe records the principal (if any) and the named header as seen by
// the next handler in the chain, so a test can assert on both in one pass.
func assertionProbe(gotPrincipal **session.Principal, gotHeader *string, header string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotHeader = r.Header.Get(header)
		if p, ok := session.FromContext(r.Context()); ok {
			*gotPrincipal = p
		} else {
			*gotPrincipal = nil
		}
	})
}

func TestAssertionGateEnrichesMatchingPrincipal(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("peer-hog", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{
		Subject:  "u-1",
		Passport: map[string]any{"email": "u1@x.co"},
		Groups:   []string{"admins"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("peer-hog", map[string]ed25519.PublicKey{"k1": pub})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, token)
	existing := &session.Principal{
		Subject: "u-1", AccessToken: "AT-1", SessionID: "sess-1",
		Passport: map[string]any{"stale": true}, Groups: []string{"old"},
	}
	req = req.WithContext(session.WithPrincipal(req.Context(), existing))

	var gotP *session.Principal
	var gotHeader string
	chain.Compose(assertionProbe(&gotP, &gotHeader, DefaultAssertionHeader), AssertionGate(v, "", true, nil)).
		ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "" {
		t.Fatalf("assertion header must be gone by the time the next handler runs, got %q", gotHeader)
	}
	if gotP == nil {
		t.Fatal("principal missing after enrichment")
	}
	if gotP.Subject != "u-1" || gotP.AccessToken != "AT-1" || gotP.SessionID != "sess-1" {
		t.Fatalf("enriched principal must keep Subject/AccessToken/SessionID, got %+v", gotP)
	}
	if gotP.Passport["email"] != "u1@x.co" {
		t.Fatalf("passport must come from the assertion, got %v", gotP.Passport)
	}
	if len(gotP.Groups) != 1 || gotP.Groups[0] != "admins" {
		t.Fatalf("groups must come from the assertion, got %v", gotP.Groups)
	}
}

func TestAssertionGateSubjectMismatchLeavesUnchanged(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("peer-hog", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-2"})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("peer-hog", map[string]ed25519.PublicKey{"k1": pub})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, token)
	existing := &session.Principal{Subject: "u-1", Passport: map[string]any{"email": "u1@x.co"}, Groups: []string{"admins"}}
	req = req.WithContext(session.WithPrincipal(req.Context(), existing))

	var gotP *session.Principal
	var gotHeader string
	chain.Compose(assertionProbe(&gotP, &gotHeader, DefaultAssertionHeader), AssertionGate(v, "", true, nil)).
		ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "" {
		t.Fatal("assertion header must be stripped even on a subject mismatch")
	}
	if gotP == nil || gotP.Subject != "u-1" || gotP.Passport["email"] != "u1@x.co" || len(gotP.Groups) != 1 || gotP.Groups[0] != "admins" {
		t.Fatalf("principal must be untouched on subject mismatch, got %+v", gotP)
	}
}

func TestAssertionGateInvalidTokenLeavesUnchanged(t *testing.T) {
	_, pub := genAssertionKeys(t)
	v := NewAssertionVerifier("peer-hog", map[string]ed25519.PublicKey{"k1": pub})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, "not-a-real-token")
	existing := &session.Principal{Subject: "u-1"}
	req = req.WithContext(session.WithPrincipal(req.Context(), existing))

	var gotP *session.Principal
	var gotHeader string
	chain.Compose(assertionProbe(&gotP, &gotHeader, DefaultAssertionHeader), AssertionGate(v, "", true, nil)).
		ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "" {
		t.Fatal("assertion header must be stripped even on an invalid token")
	}
	if gotP == nil || gotP.Subject != "u-1" {
		t.Fatalf("principal must be untouched on an invalid token, got %+v", gotP)
	}
}

func TestAssertionGateNoPrincipalRequireBearerTrueProducesNone(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("peer-hog", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("peer-hog", map[string]ed25519.PublicKey{"k1": pub})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, token)

	var gotP *session.Principal
	var gotHeader string
	chain.Compose(assertionProbe(&gotP, &gotHeader, DefaultAssertionHeader), AssertionGate(v, "", true, nil)).
		ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "" {
		t.Fatal("assertion header must be stripped")
	}
	if gotP != nil {
		t.Fatalf("requireBearer=true must not authenticate on the assertion alone, got %+v", gotP)
	}
}

func TestAssertionGateNoPrincipalRequireBearerFalseProducesOne(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("peer-hog", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{
		Subject: "u-1", Passport: map[string]any{"email": "u1@x.co"}, Groups: []string{"admins"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("peer-hog", map[string]ed25519.PublicKey{"k1": pub})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, token)

	var gotP *session.Principal
	var gotHeader string
	chain.Compose(assertionProbe(&gotP, &gotHeader, DefaultAssertionHeader), AssertionGate(v, "", false, nil)).
		ServeHTTP(httptest.NewRecorder(), req)

	if gotP == nil {
		t.Fatal("requireBearer=false must authenticate on the assertion alone")
	}
	if gotP.Subject != "u-1" || gotP.Passport["email"] != "u1@x.co" || len(gotP.Groups) != 1 || gotP.Groups[0] != "admins" {
		t.Fatalf("principal built from the assertion alone = %+v", gotP)
	}
	if gotP.AccessToken != "" {
		t.Fatalf("assertion-only principal must not carry an access token, got %q", gotP.AccessToken)
	}
}

func TestIssueAssertionReplacesClientSuppliedHeader(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, "spoofed")
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject: "u-1", Passport: map[string]any{"email": "u1@x.co"}, Groups: []string{"admins"},
	}))

	var gotHeader string
	chain.Compose(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(DefaultAssertionHeader)
	}), IssueAssertion(iss, "", nil)).ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader == "" || gotHeader == "spoofed" {
		t.Fatalf("client-supplied header must be replaced with a freshly minted assertion, got %q", gotHeader)
	}
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	a, err := v.Verify(gotHeader)
	if err != nil {
		t.Fatalf("issued header does not verify: %v", err)
	}
	if a.Subject != "u-1" {
		t.Fatalf("asserted subject = %q, want u-1", a.Subject)
	}
}

func TestIssueAssertionStripsHeaderWhenNoPrincipal(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultAssertionHeader, "spoofed")

	var gotHeader string
	chain.Compose(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(DefaultAssertionHeader)
	}), IssueAssertion(iss, "", nil)).ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "" {
		t.Fatalf("spoofed header must be stripped and nothing issued when there is no principal, got %q", gotHeader)
	}
}
