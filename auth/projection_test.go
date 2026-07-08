package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/route"
	"github.com/paulopiriquito/hog/session"
)

// headerEcho records the request headers the terminal received.
func headerEcho(into *http.Header) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { *into = r.Header.Clone() })
}

func TestProjectionDeriveAndStrip(t *testing.T) {
	var got http.Header
	gate := ProjectionGate(nil, "groups")
	h := chain.Compose(headerEcho(&got), gate)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-Id", "SPOOFED") // inbound spoof must be stripped
	req.Header.Set("X-User-Evil", "x")
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject:  "u-1",
		Passport: map[string]any{"email": "a@b.co", "given_name": "Al"},
		Groups:   []string{"r1", "r2"},
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.Get("X-User-Id") != "u-1" {
		t.Fatalf("X-User-Id = %q (spoof not replaced)", got.Get("X-User-Id"))
	}
	if got.Get("X-User-Email") != "a@b.co" || got.Get("X-User-Given-Name") != "Al" {
		t.Fatalf("claim headers = %v", got)
	}
	if got.Get("X-User-Groups") != "r1,r2" {
		t.Fatalf("groups header = %q", got.Get("X-User-Groups"))
	}
	if got.Get("X-User-Evil") != "" {
		t.Fatalf("inbound X-User-Evil not stripped")
	}
}

func TestProjectionUnauthenticatedOnlyStrips(t *testing.T) {
	var got http.Header
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-Id", "SPOOFED")
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Id") != "" {
		t.Fatalf("unauth must strip and not inject, got X-User-Id=%q", got.Get("X-User-Id"))
	}
}

func TestProjectionGroupsAsAndOverride(t *testing.T) {
	var got http.Header
	// groupsAs "roles" ⇒ X-User-Roles (no override)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{Subject: "u", Groups: []string{"a"}}))
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "roles")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Roles") != "a" {
		t.Fatalf("As-derived groups header = %v", got)
	}

	// override: only mapped claims + custom group header
	var got2 http.Header
	proj := &route.ProjectionConfig{Session: &route.SessionProjection{
		Claims: map[string]string{"email": "X-Auth-Email"},
		Groups: &route.GroupsProjection{Header: "X-Group"},
	}}
	req2 := httptest.NewRequest("GET", "/", nil)
	req2 = req2.WithContext(session.WithPrincipal(req2.Context(), &session.Principal{
		Subject: "u", Passport: map[string]any{"email": "e@x.co", "name": "N"}, Groups: []string{"g"}}))
	chain.Compose(headerEcho(&got2), ProjectionGate(proj, "groups")).ServeHTTP(httptest.NewRecorder(), req2)
	if got2.Get("X-Auth-Email") != "e@x.co" {
		t.Fatalf("override email header = %q", got2.Get("X-Auth-Email"))
	}
	if got2.Get("X-User-Name") != "" {
		t.Fatal("override must NOT project unlisted claims")
	}
	if got2.Get("X-User-Id") != "u" {
		t.Fatal("X-User-Id always set")
	}
	if got2.Get("X-Group") != "g" {
		t.Fatalf("override group header = %q", got2.Get("X-Group"))
	}
}

func TestCanon(t *testing.T) {
	cases := map[string]string{"email": "Email", "given_name": "Given-Name", "groups": "Groups", "preferred-username": "Preferred-Username"}
	for in, want := range cases {
		if got := canon(in); got != want {
			t.Errorf("canon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectionOverrideAbsentClaimStripsSpoof(t *testing.T) {
	var got http.Header
	proj := &route.ProjectionConfig{Session: &route.SessionProjection{
		Claims: map[string]string{"email": "X-Auth-Email"}, // target OUTSIDE X-User-*
	}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Auth-Email", "attacker@evil.co") // inbound spoof
	req = req.WithContext(session.WithPrincipal(req.Context(),
		&session.Principal{Subject: "u", Passport: map[string]any{"name": "N"}})) // NO email claim
	chain.Compose(headerEcho(&got), ProjectionGate(proj, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-Auth-Email") != "" {
		t.Fatalf("absent-claim override must clear inbound spoof, got %q", got.Get("X-Auth-Email"))
	}
}

func TestProjectionGroupsOverrideEmptyGroupsStripsSpoof(t *testing.T) {
	var got http.Header
	proj := &route.ProjectionConfig{Session: &route.SessionProjection{
		Groups: &route.GroupsProjection{Header: "X-Group"},
	}}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Group", "admins") // inbound spoof
	req = req.WithContext(session.WithPrincipal(req.Context(),
		&session.Principal{Subject: "u"})) // no groups
	chain.Compose(headerEcho(&got), ProjectionGate(proj, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-Group") != "" {
		t.Fatalf("empty groups must clear spoofed custom groups header, got %q", got.Get("X-Group"))
	}
}

func TestProjectionDeriveClaimCannotClobberSubjectOrGroups(t *testing.T) {
	var got http.Header
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject:  "u-1",
		Passport: map[string]any{"id": "EVIL", "groups": "EVIL-GROUPS", "email": "e@x.co"},
		Groups:   []string{"real"},
	}))
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Id") != "u-1" {
		t.Fatalf("subject must win over claim named id, got %q", got.Get("X-User-Id"))
	}
	if got.Get("X-User-Groups") != "real" {
		t.Fatalf("real groups must win over claim named groups, got %q", got.Get("X-User-Groups"))
	}
	if got.Get("X-User-Email") != "e@x.co" {
		t.Fatalf("unrelated claim still derived, got %q", got.Get("X-User-Email"))
	}
}

func TestProjectionRejectsControlCharClaim(t *testing.T) {
	var got http.Header
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject:  "u",
		Passport: map[string]any{"email": "a@b.co\r\nX-User-Id: admin"},
	}))
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Email") != "" {
		t.Fatalf("control-char claim value must be dropped, got %q", got.Get("X-User-Email"))
	}
	if got.Get("X-User-Id") != "u" {
		t.Fatalf("subject intact, got %q", got.Get("X-User-Id"))
	}
}

func TestProjectionDropsInvalidHeaderName(t *testing.T) {
	var got http.Header
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject:  "u",
		Passport: map[string]any{"foo bar": "x", "email": "e@x.co"}, // "foo bar" → invalid header token
	}))
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Foo Bar") != "" {
		t.Fatalf("invalid header name must be dropped, got %q", got.Get("X-User-Foo Bar"))
	}
	if got.Get("X-User-Email") != "e@x.co" {
		t.Fatalf("valid claim still derived, got %q", got.Get("X-User-Email"))
	}
}

func TestProjectionDeriveSkipsNonScalarClaim(t *testing.T) {
	var got http.Header
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{
		Subject:  "u",
		Passport: map[string]any{"roles": []any{"a", "b"}, "email": "e@x.co"},
	}))
	chain.Compose(headerEcho(&got), ProjectionGate(nil, "groups")).ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("X-User-Roles") != "" {
		t.Fatalf("non-scalar claim must be skipped, got %q", got.Get("X-User-Roles"))
	}
	if got.Get("X-User-Email") != "e@x.co" {
		t.Fatalf("scalar claim still derived, got %q", got.Get("X-User-Email"))
	}
}
