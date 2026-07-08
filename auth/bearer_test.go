package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/session"
)

// fakeBearer is a minimal BearerVerifier double.
type fakeBearer struct {
	id   *idp.Identity
	err  error
	ui   map[string]any
	uerr error
	uiN  int
}

func (f *fakeBearer) VerifyAccessToken(ctx context.Context, raw string) (*idp.Identity, error) {
	return f.id, f.err
}
func (f *fakeBearer) UserInfo(ctx context.Context, at string) (map[string]any, error) {
	f.uiN++
	return f.ui, f.uerr
}

func probe(seen *bool, subj *string, groups *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := session.FromContext(r.Context()); ok {
			*seen = true
			*subj = p.Subject
			if len(p.Groups) > 0 {
				*groups = p.Groups[0]
			}
		}
		if hasBearerError(r.Context()) {
			w.Header().Set("X-Probe-Bearer-Error", "1")
		}
	})
}

func TestBearerGateValidTokenOnly(t *testing.T) {
	v := &fakeBearer{id: &idp.Identity{Subject: "u-1", Claims: map[string]any{"email": "e@x.co"}}}
	idCfg := session.IdentityConfig{Claims: []string{"email"}, UserInfo: "auto"}
	var seen bool
	var subj, grp string
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, idCfg, nil)).ServeHTTP(httptest.NewRecorder(), req)
	if !seen || subj != "u-1" {
		t.Fatalf("principal not resolved: seen=%v subj=%q", seen, subj)
	}
	if v.uiN != 0 {
		t.Fatalf("userinfo should not be called when token has the claims, got %d", v.uiN)
	}
}

func TestBearerGateUserInfoFallback(t *testing.T) {
	v := &fakeBearer{
		id: &idp.Identity{Subject: "u-1", Claims: map[string]any{}},
		ui: map[string]any{"isMemberOf": []any{"cn=admins,ou=applicationRole"}},
	}
	idCfg := session.IdentityConfig{
		Groups:   &session.GroupsConfig{Source: "isMemberOf", Match: []string{"ou=applicationRole"}, Render: "cn", As: "groups"},
		UserInfo: "auto",
	}
	var seen bool
	var subj, grp string
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, idCfg, nil)).ServeHTTP(httptest.NewRecorder(), req)
	if v.uiN != 1 {
		t.Fatalf("userinfo should be called once, got %d", v.uiN)
	}
	if grp != "admins" {
		t.Fatalf("group from userinfo = %q", grp)
	}
}

func TestBearerGateUserInfoFailureBestEffort(t *testing.T) {
	v := &fakeBearer{
		id:   &idp.Identity{Subject: "u-1", Claims: map[string]any{}},
		uerr: errors.New("userinfo down"),
	}
	idCfg := session.IdentityConfig{Groups: &session.GroupsConfig{Source: "isMemberOf"}, UserInfo: "auto"}
	var seen bool
	var subj, grp string
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, idCfg, nil)).ServeHTTP(httptest.NewRecorder(), req)
	if !seen || subj != "u-1" {
		t.Fatal("must proceed best-effort with a token-only principal")
	}
}

func TestBearerGateInvalidTokenFlags(t *testing.T) {
	v := &fakeBearer{err: errors.New("bad token")}
	var seen bool
	var subj, grp string
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, session.IdentityConfig{}, nil)).ServeHTTP(rec, req)
	if seen {
		t.Fatal("invalid token must not yield a principal")
	}
	if rec.Header().Get("X-Probe-Bearer-Error") != "1" {
		t.Fatal("invalid_token flag not set in context")
	}
}

func TestBearerGateEmptySubjectRejected(t *testing.T) {
	v := &fakeBearer{id: &idp.Identity{Subject: "", Claims: map[string]any{"email": "e@x.co"}}}
	var seen bool
	var subj, grp string
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, session.IdentityConfig{}, nil)).ServeHTTP(rec, req)
	if seen {
		t.Fatal("a token with no subject must not yield a principal")
	}
	if rec.Header().Get("X-Probe-Bearer-Error") != "1" {
		t.Fatal("empty-subject token should flag invalid_token (fail-closed)")
	}
}

func TestBearerGateNilIdentityRejected(t *testing.T) {
	v := &fakeBearer{id: nil, err: nil} // verifier returns (nil, nil)
	var seen bool
	var subj, grp string
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, session.IdentityConfig{}, nil)).ServeHTTP(rec, req)
	if seen {
		t.Fatal("nil identity must not yield a principal")
	}
	if rec.Header().Get("X-Probe-Bearer-Error") != "1" {
		t.Fatal("nil identity should flag invalid_token (fail-closed)")
	}
}

func TestBearerGateNoHeaderAndCookieWins(t *testing.T) {
	v := &fakeBearer{id: &idp.Identity{Subject: "bearer"}}
	// no Authorization header ⇒ pass through, no principal
	var seen bool
	var subj, grp string
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, session.IdentityConfig{}, nil)).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen {
		t.Fatal("no header must not resolve a principal")
	}
	// cookie already authenticated ⇒ bearer skipped (principal unchanged)
	seen, subj = false, ""
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	req = req.WithContext(session.WithPrincipal(req.Context(), &session.Principal{Subject: "cookie"}))
	chain.Compose(probe(&seen, &subj, &grp), BearerGate(v, session.IdentityConfig{}, nil)).
		ServeHTTP(httptest.NewRecorder(), req)
	if subj != "cookie" {
		t.Fatalf("cookie principal must win over bearer, got %q", subj)
	}
}
