package terminal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/session"
)

func inboundReq(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "http://hog.example/app/x", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	r.Header.Set("Cookie", "hog_session=abc; other=1")
	r.Header.Set("X-User-Id", "u-1")
	r.Header.Set("X-User-Groups", "admins")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.Header.Set("X-Forwarded-Proto", "https")
	return r
}

func newOut(t *testing.T, in *http.Request) *http.Request {
	t.Helper()
	out, err := http.NewRequestWithContext(in.Context(), "GET", "http://backend.svc/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPrepareStripsCookiesAndForwardsIdentity(t *testing.T) {
	in := inboundReq(t)
	out := newOut(t, in)
	out.Header.Set("Cookie", in.Header.Get("Cookie")) // simulate the proxy clone: out starts with the inbound cookie
	prepareBackendRequest(out, in, forwardOptions{})

	if out.Header.Get("Cookie") != "" {
		t.Fatalf("cookie not stripped: %q", out.Header.Get("Cookie"))
	}
	if out.Header.Get("X-User-Id") != "u-1" || out.Header.Get("X-User-Groups") != "admins" {
		t.Fatalf("identity not forwarded: %v", out.Header)
	}
	if out.Header.Get("Authorization") != "" {
		t.Fatal("no token must be injected without forwardAccessToken")
	}
	if got := out.Header.Get("X-Forwarded-For"); got != "203.0.113.7, 10.0.0.5" {
		t.Fatalf("XFF = %q", got)
	}
	if out.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("XFP = %q", out.Header.Get("X-Forwarded-Proto"))
	}
	if out.Header.Get("X-Forwarded-Host") != "hog.example" {
		t.Fatalf("XFH = %q", out.Header.Get("X-Forwarded-Host"))
	}
}

func TestPrepareForwardCookiesOptIn(t *testing.T) {
	in := inboundReq(t)
	out := newOut(t, in)
	out.Header.Set("Cookie", in.Header.Get("Cookie")) // proxy clones inbound headers
	prepareBackendRequest(out, in, forwardOptions{forwardCookies: true})
	if out.Header.Get("Cookie") == "" {
		t.Fatal("forwardCookies must keep the cookie header")
	}
}

func TestPrepareInjectsAccessTokenOnlyWhenPrincipalPresent(t *testing.T) {
	in := inboundReq(t)
	in = in.WithContext(session.WithPrincipal(in.Context(), &session.Principal{Subject: "u-1", AccessToken: "AT-123"}))
	out := newOut(t, in)
	prepareBackendRequest(out, in, forwardOptions{forwardAccessToken: true})
	if out.Header.Get("Authorization") != "Bearer AT-123" {
		t.Fatalf("Authorization = %q", out.Header.Get("Authorization"))
	}

	in2 := inboundReq(t)
	out2 := newOut(t, in2)
	prepareBackendRequest(out2, in2, forwardOptions{forwardAccessToken: true})
	if out2.Header.Get("Authorization") != "" {
		t.Fatal("no principal => no Authorization")
	}

	in3 := inboundReq(t)
	in3 = in3.WithContext(session.WithPrincipal(in3.Context(), &session.Principal{Subject: "u-1", AccessToken: ""}))
	out3 := newOut(t, in3)
	prepareBackendRequest(out3, in3, forwardOptions{forwardAccessToken: true})
	if out3.Header.Get("Authorization") != "" {
		t.Fatal("empty AccessToken must not inject Authorization")
	}
}

// TestPrepareStripsClientAuthorizationUnlessForwarded verifies that HOG is the
// sole source of a backend Authorization header: a client-supplied bearer in the
// cloned outbound request is always stripped, and only forwardAccessToken can
// inject one (via the session principal).
func TestPrepareStripsClientAuthorizationUnlessForwarded(t *testing.T) {
	// Case 1: forwardAccessToken=false — any pre-set Authorization on out must be stripped.
	t.Run("stripped_when_not_forwarding", func(t *testing.T) {
		in := inboundReq(t)
		in.Header.Set("Authorization", "Bearer client-injected")
		out := newOut(t, in)
		out.Header.Set("Authorization", "Bearer client-injected") // simulate ReverseProxy clone
		prepareBackendRequest(out, in, forwardOptions{})
		if got := out.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization must be stripped when forwardAccessToken=false, got %q", got)
		}
	})

	// Case 2: forwardAccessToken=true WITH a principal — the principal's token
	// must overwrite any pre-set inbound Authorization.
	t.Run("principal_token_overwrites_client_bearer", func(t *testing.T) {
		in := inboundReq(t)
		in.Header.Set("Authorization", "Bearer client-injected")
		in = in.WithContext(session.WithPrincipal(in.Context(), &session.Principal{Subject: "u-1", AccessToken: "tok"}))
		out := newOut(t, in)
		out.Header.Set("Authorization", "Bearer client-injected") // simulate ReverseProxy clone
		prepareBackendRequest(out, in, forwardOptions{forwardAccessToken: true})
		if got := out.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("Authorization = %q, want Bearer tok", got)
		}
	})

	// Case 3: forwardAccessToken=true but NO principal in context — Authorization
	// must be stripped and nothing injected.
	t.Run("no_principal_strips_inbound", func(t *testing.T) {
		in := inboundReq(t)
		in.Header.Set("Authorization", "Bearer client-injected")
		out := newOut(t, in)
		out.Header.Set("Authorization", "Bearer client-injected") // simulate ReverseProxy clone
		prepareBackendRequest(out, in, forwardOptions{forwardAccessToken: true})
		if got := out.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization must be stripped when no principal, got %q", got)
		}
	})
}
