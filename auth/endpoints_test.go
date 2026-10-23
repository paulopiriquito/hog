package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/v2/idp"
	"github.com/paulopiriquito/hog/v2/session"
)

// fakeIdP is a controllable idp.IdP test double.
type fakeIdP struct {
	pkce      bool
	exchID    *idp.Identity
	exchTok   *idp.Tokens
	exchErr   error
	userinfo  map[string]any
	uiErr     error
	lastVerif string
	lastNonce string
}

func (f *fakeIdP) AuthCodeURL(state, nonce, codeVerifier string) string {
	q := url.Values{"state": {state}, "nonce": {nonce}}
	if f.pkce && codeVerifier != "" {
		q.Set("code_challenge", "challenge")
		q.Set("code_challenge_method", "S256")
	}
	return "https://idp.example/authorize?" + q.Encode()
}
func (f *fakeIdP) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*idp.Tokens, *idp.Identity, error) {
	f.lastVerif, f.lastNonce = codeVerifier, nonce
	if f.exchErr != nil {
		return nil, nil, f.exchErr
	}
	return f.exchTok, f.exchID, nil
}
func (f *fakeIdP) Refresh(ctx context.Context, rt string) (*idp.Tokens, error) { return f.exchTok, nil }
func (f *fakeIdP) Verify(ctx context.Context, raw string) (*idp.Identity, error) {
	return f.exchID, nil
}
func (f *fakeIdP) VerifyAccessToken(ctx context.Context, raw string) (*idp.Identity, error) {
	return f.Verify(ctx, raw)
}
func (f *fakeIdP) LogoutURL(hint, redir string) (string, bool) { return "", false }
func (f *fakeIdP) UserInfo(ctx context.Context, at string) (map[string]any, error) {
	return f.userinfo, f.uiErr
}
func (f *fakeIdP) UsesPKCE() bool { return f.pkce }

func testHandlers(t *testing.T, f *fakeIdP, sessMut func(*session.Config)) (*Handlers, session.Manager, *session.Sealer) {
	t.Helper()
	sc := session.Config{
		CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour,
		FingerprintHeaders: []string{"User-Agent"}, PassportClaims: []string{"email", "name"},
		InfoPath: "/auth/session", PostLogoutRedirect: "/bye",
	}
	if sessMut != nil {
		sessMut(&sc)
	}
	m, err := session.NewManager(sc)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := session.NewSealer(sc.Key)
	if err != nil {
		t.Fatal(err)
	}
	idCfg := session.IdentityConfig{Claims: sc.PassportClaims, Groups: sc.Groups, UserInfo: "auto"}
	h := NewHandlers(f, m, sealer, Config{LoginPath: "/auth/login", LogoutPath: "/auth/logout"}, sc, idCfg)
	return h, m, sealer
}

func TestLoginSetsStateCookieAndRedirects(t *testing.T) {
	f := &fakeIdP{pkce: true}
	h, _, sealer := testHandlers(t, f, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/login?return_to=/app/x", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.Login(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, _ := url.Parse(loc)
	if u.Query().Get("state") == "" || u.Query().Get("nonce") == "" || u.Query().Get("code_challenge") == "" {
		t.Fatalf("redirect missing params: %s", loc)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginCookieName(u.Query().Get("state")) {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie wrong: %+v", cookie)
	}
	ls, err := openLoginState(sealer, cookie.Value)
	if err != nil || ls.ReturnTo != "/app/x" || ls.State != u.Query().Get("state") || ls.Verifier == "" {
		t.Fatalf("login state = %+v err=%v", ls, err)
	}
}

func TestLoginPKCEOffNoVerifier(t *testing.T) {
	f := &fakeIdP{pkce: false}
	h, _, sealer := testHandlers(t, f, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/login", nil)
	req.Header.Set("User-Agent", "UA")
	h.Login(rec, req)
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, loginCookie+"_") {
			ls, _ := openLoginState(sealer, c.Value)
			if ls.Verifier != "" {
				t.Fatal("PKCE off: verifier must be empty")
			}
			if ls.ReturnTo != "/" {
				t.Fatalf("default returnTo = %q", ls.ReturnTo)
			}
		}
	}
	if strings.Contains(rec.Header().Get("Location"), "code_challenge") {
		t.Fatal("PKCE off: no challenge in redirect")
	}
}

func doLogin(t *testing.T, h *Handlers) []*http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/login?return_to=/app/home", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.Login(rec, req)
	return rec.Result().Cookies()
}

func loginCookieValue(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, loginCookie+"_") {
			return c.Value
		}
	}
	return ""
}

func mustSealer(t *testing.T) *session.Sealer {
	t.Helper()
	s, err := session.NewSealer([]byte(key32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCallbackSuccess(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:  &idp.Identity{Subject: "u-1", Claims: map[string]any{"name": "Alice"}},
		exchTok: &idp.Tokens{AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)},
	}
	h, _, sealer := testHandlers(t, f, nil)
	cookies := doLogin(t, h)
	ls, _ := openLoginState(sealer, loginCookieValue(cookies))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state="+ls.State, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	h.Callback(rec, req)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/app/home" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	var gotSession bool
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "hog_session") && c.MaxAge >= 0 {
			gotSession = true
		}
	}
	if !gotSession {
		t.Fatal("no session cookie written")
	}
	if f.lastVerif != ls.Verifier || f.lastNonce != ls.Nonce {
		t.Fatalf("exchange got verifier/nonce %q/%q", f.lastVerif, f.lastNonce)
	}
}

func TestCallbackStateMismatch(t *testing.T) {
	f := &fakeIdP{pkce: true, exchID: &idp.Identity{Subject: "u"}, exchTok: &idp.Tokens{AccessToken: "at"}}
	h, _, _ := testHandlers(t, f, nil)
	cookies := doLogin(t, h)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state=WRONG", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("state mismatch status = %d, want 400", rec.Code)
	}
}

func TestCallbackNoLoginCookie(t *testing.T) {
	f := &fakeIdP{pkce: true}
	h, _, _ := testHandlers(t, f, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state=x", nil)
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no login cookie status = %d, want 400", rec.Code)
	}
}

func TestCallbackFetchesUserInfoWhenNeeded(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:   &idp.Identity{Subject: "u-1", Claims: map[string]any{}},
		exchTok:  &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)},
		userinfo: map[string]any{"isMemberOf": []any{"cn=role-x,ou=applicationRole"}},
	}
	h, m, _ := testHandlers(t, f, func(sc *session.Config) {
		sc.Groups = &session.GroupsConfig{Source: "isMemberOf", Match: []string{"ou=applicationRole"}, Render: "cn", As: "groups"}
	})
	cookies := doLogin(t, h)
	ls, _ := openLoginState(mustSealer(t), loginCookieValue(cookies))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state="+ls.State, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	h.Callback(rec, req)
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "Mozilla/5.0")
	for _, c := range rec.Result().Cookies() {
		rq.AddCookie(c)
	}
	s, err := m.Read(rq)
	if err != nil || len(s.Groups) != 1 || s.Groups[0] != "role-x" {
		t.Fatalf("groups from userinfo = %+v err=%v", s, err)
	}
}

func TestLogout(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:  &idp.Identity{Subject: "u-1"},
		exchTok: &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)},
	}
	h, _, _ := testHandlers(t, f, nil)
	// establish a session via callback
	cookies := doLogin(t, h)
	ls, _ := openLoginState(mustSealer(t), loginCookieValue(cookies))
	rc := httptest.NewRecorder()
	cb := httptest.NewRequest("GET", "/auth/callback?code=abc&state="+ls.State, nil)
	cb.Header.Set("User-Agent", "Mozilla/5.0")
	for _, c := range cookies {
		cb.AddCookie(c)
	}
	h.Callback(rc, cb)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/logout", nil)
	for _, c := range rc.Result().Cookies() {
		req.AddCookie(c)
	}
	h.Logout(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/bye" {
		t.Fatalf("logout status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "hog_session") && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("session cookie not cleared on logout")
	}
}

func completeCallback(t *testing.T, h *Handlers, state string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	h.Callback(rec, req)
	return rec
}

func TestCallbackCompletesWhileAnotherSignInIsInFlight(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:  &idp.Identity{Subject: "u-1"},
		exchTok: &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)},
	}
	h, _, sealer := testHandlers(t, f, nil)
	first := doLogin(t, h)
	// A second sign-in starts before the first returns, e.g. a favicon request
	// or another tab. It must not overwrite the first one's state.
	second := doLogin(t, h)
	ls, _ := openLoginState(sealer, loginCookieValue(first))

	rec := completeCallback(t, h, ls.State, append(first, second...))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/app/home" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == second[0].Name {
			t.Fatalf("callback touched the other sign-in's cookie: %+v", c)
		}
		if c.Name == first[0].Name && c.MaxAge >= 0 {
			t.Fatalf("callback left its own login cookie alive: %+v", c)
		}
	}
}

func TestCallbackReadsPlainLoginCookie(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:  &idp.Identity{Subject: "u-1"},
		exchTok: &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)},
	}
	h, _, sealer := testHandlers(t, f, nil)
	// Written by an instance from before sign-ins had their own cookie.
	sealed, err := sealLoginState(sealer, loginState{State: "st-1", Nonce: "n", Verifier: "v", ReturnTo: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	rec := completeCallback(t, h, "st-1", []*http.Cookie{{Name: loginCookie, Value: sealed}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/x" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	var expired bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginCookie && c.MaxAge < 0 {
			expired = true
		}
	}
	if !expired {
		t.Fatal("plain login cookie not expired")
	}
}

func TestCallbackUnmatchedWithSessionGoesToApp(t *testing.T) {
	f := &fakeIdP{pkce: true,
		exchID:  &idp.Identity{Subject: "u-1"},
		exchTok: &idp.Tokens{AccessToken: "at", Expiry: time.Now().Add(time.Hour)},
	}
	h, _, sealer := testHandlers(t, f, nil)
	cookies := doLogin(t, h)
	ls, _ := openLoginState(sealer, loginCookieValue(cookies))
	signedIn := completeCallback(t, h, ls.State, cookies)
	var session []*http.Cookie
	for _, c := range signedIn.Result().Cookies() {
		if strings.HasPrefix(c.Name, "hog_session") && c.MaxAge >= 0 {
			session = append(session, c)
		}
	}

	for _, state := range []string{ls.State, "WRONG"} {
		f.exchID = nil
		rec := completeCallback(t, h, state, session)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
			t.Fatalf("state %q: status=%d loc=%q", state, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := completeCallback(t, h, "WRONG", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("no session: status = %d, want 400", rec.Code)
	}
}

func TestLoginClearsLoginCookiesPastTheLimit(t *testing.T) {
	h, _, _ := testHandlers(t, &fakeIdP{pkce: true}, nil)
	req := httptest.NewRequest("GET", "/auth/login", nil)
	var stale []string
	for i := range maxLoginFlows {
		name := loginCookieName(string(rune('a' + i)))
		stale = append(stale, name)
		req.AddCookie(&http.Cookie{Name: name, Value: "x"})
	}
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	expired := map[string]bool{}
	var issued int
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			expired[c.Name] = true
		} else if strings.HasPrefix(c.Name, loginCookie+"_") {
			issued++
		}
	}
	for _, name := range stale {
		if !expired[name] {
			t.Fatalf("%s not expired; got %v", name, expired)
		}
	}
	if issued != 1 {
		t.Fatalf("issued %d login cookies, want 1", issued)
	}
}
