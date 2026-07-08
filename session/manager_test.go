package session

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/idp"
)

func testManager(t *testing.T, mut func(*Config)) Manager {
	t.Helper()
	cfg := Config{
		CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour,
		FingerprintHeaders: []string{"User-Agent"},
		PassportClaims:     []string{"email", "name"},
		Groups:             &GroupsConfig{Source: "isMemberOf", Match: []string{"ou=applicationRole"}, Render: "cn", As: "groups"},
		InfoPath:           "/auth/session", PostLogoutRedirect: "/",
	}
	if mut != nil {
		mut(&cfg)
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func login(r *http.Request) *http.Request {
	r.Header.Set("User-Agent", "Mozilla/5.0")
	return r
}

func TestManagerRoundTripAndDiscardsRefreshToken(t *testing.T) {
	m := testManager(t, nil)
	idt := &idp.Identity{Subject: "u-9", Claims: map[string]any{"name": "Alice"}}
	userinfo := map[string]any{
		"email":      "alice@x.co",
		"isMemberOf": []any{"cn=PT-LM-ROLE-app-admin,ou=app,ou=applicationRole,ou=role,ou=PT-LM,o=corp"},
	}
	tok := &idp.Tokens{AccessToken: "at", RefreshToken: "rt-SECRET", Expiry: time.Now().Add(time.Hour)}

	wr := httptest.NewRecorder()
	rq := login(httptest.NewRequest("GET", "/", nil))
	if err := m.Issue(wr, rq, idt, userinfo, tok); err != nil {
		t.Fatal(err)
	}
	for _, c := range wr.Result().Cookies() {
		if containsStr(c.Value, "rt-SECRET") {
			t.Fatal("refresh token leaked into cookie")
		}
	}
	rq2 := login(httptest.NewRequest("GET", "/", nil))
	for _, c := range wr.Result().Cookies() {
		rq2.AddCookie(c)
	}
	got, err := m.Read(rq2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Subject != "u-9" || got.AccessToken != "at" || got.Passport["email"] != "alice@x.co" || got.Passport["name"] != "Alice" {
		t.Fatalf("read session = %+v", got)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "PT-LM-ROLE-app-admin" {
		t.Fatalf("groups = %v", got.Groups)
	}
}

func TestManagerReadErrors(t *testing.T) {
	m := testManager(t, nil)
	if _, err := m.Read(httptest.NewRequest("GET", "/", nil)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(&http.Cookie{Name: "hog_session.0", Value: "@@@garbage@@@"})
	if _, err := m.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("want ErrInvalidSession, got %v", err)
	}
}

func TestManagerExpiredAndFingerprintMismatch(t *testing.T) {
	idt := &idp.Identity{Subject: "u-1"}
	tok := &idp.Tokens{AccessToken: "at"}

	mExp := testManager(t, func(c *Config) { c.TTL = -time.Minute })
	wr := httptest.NewRecorder()
	rq := login(httptest.NewRequest("GET", "/", nil))
	if err := mExp.Issue(wr, rq, idt, nil, tok); err != nil {
		t.Fatal(err)
	}
	rq2 := login(httptest.NewRequest("GET", "/", nil))
	for _, c := range wr.Result().Cookies() {
		rq2.AddCookie(c)
	}
	if _, err := mExp.Read(rq2); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired want ErrInvalidSession, got %v", err)
	}

	m := testManager(t, nil)
	wr2 := httptest.NewRecorder()
	rqA := login(httptest.NewRequest("GET", "/", nil))
	if err := m.Issue(wr2, rqA, idt, nil, tok); err != nil {
		t.Fatal(err)
	}
	rqB := httptest.NewRequest("GET", "/", nil)
	rqB.Header.Set("User-Agent", "curl/8")
	for _, c := range wr2.Result().Cookies() {
		rqB.AddCookie(c)
	}
	if _, err := m.Read(rqB); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("fingerprint mismatch want ErrInvalidSession, got %v", err)
	}
}

func TestManagerClear(t *testing.T) {
	m := testManager(t, nil)
	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(&http.Cookie{Name: "hog_session.0", Value: "x"})
	wr := httptest.NewRecorder()
	m.Clear(wr, rq)
	cs := wr.Result().Cookies()
	if len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Fatalf("clear did not expire chunk: %+v", cs)
	}
}
