package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/idp"
)

func stateMgr(t *testing.T, store StateStore, mut func(*Config)) Manager {
	t.Helper()
	cfg := Config{
		CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour,
		FingerprintHeaders: []string{"User-Agent"}, PassportClaims: []string{"email"},
	}
	if mut != nil {
		mut(&cfg)
	}
	m, err := NewStateManager(cfg, store, nil, 60*time.Second, "hog:sess:")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func issueState(t *testing.T, m Manager) []*http.Cookie {
	t.Helper()
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	idt := &idp.Identity{Subject: "u-1", Claims: map[string]any{"email": "a@b.co"}}
	tok := &idp.Tokens{AccessToken: "at", RefreshToken: "rt-SECRET", Expiry: time.Now().Add(time.Hour)}
	if err := m.Issue(wr, rq, idt, nil, tok); err != nil {
		t.Fatal(err)
	}
	return wr.Result().Cookies()
}

func TestStateManagerRoundTrip(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil)
	cookies := issueState(t, m)

	if len(cookies) != 1 || cookies[0].Name != "hog_session" || cookies[0].Value == "" {
		t.Fatalf("cookie = %+v", cookies)
	}
	// the stored bytes are sealed: no plaintext refresh token, not JSON-readable
	for _, e := range store.m {
		if bytes.Contains(e.val, []byte("rt-SECRET")) || bytes.Contains(e.val, []byte("\"sub\"")) {
			t.Fatal("record not encrypted at rest")
		}
	}

	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	rq.AddCookie(cookies[0])
	got, err := m.Read(rq)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Subject != "u-1" || got.AccessToken != "at" || got.Passport["email"] != "a@b.co" {
		t.Fatalf("session = %+v", got)
	}
}

func TestStateManagerReadErrors(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil)

	if _, err := m.Read(httptest.NewRequest("GET", "/", nil)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(&http.Cookie{Name: "hog_session", Value: "nope"})
	if _, err := m.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("want ErrInvalidSession, got %v", err)
	}
}

func TestStateManagerFingerprintAndExpiry(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil)
	cookies := issueState(t, m)

	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "curl/8")
	rq.AddCookie(cookies[0])
	if _, err := m.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("fingerprint want ErrInvalidSession, got %v", err)
	}

	mExp := stateMgr(t, store, func(c *Config) { c.TTL = -time.Minute })
	expCookies := issueState(t, mExp)
	rq2 := httptest.NewRequest("GET", "/", nil)
	rq2.Header.Set("User-Agent", "UA")
	rq2.AddCookie(expCookies[0])
	if _, err := mExp.Read(rq2); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired want ErrInvalidSession, got %v", err)
	}
}

func TestStateManagerClearDeletesAndExpires(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil)
	cookies := issueState(t, m)

	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(cookies[0])
	m.Clear(wr, rq)

	if len(store.m) != 0 {
		t.Fatalf("record not deleted: %d left", len(store.m))
	}
	cs := wr.Result().Cookies()
	if len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Fatalf("clear did not expire cookie: %+v", cs)
	}
}

func TestStateManagerDifferentKeyCannotRead(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil) // uses key32
	cookies := issueState(t, m)

	// a second manager with a DIFFERENT key, same store + keyPrefix
	otherKey := bytes.Repeat([]byte("z"), 32)
	m2, err := NewStateManager(
		Config{CookieName: "hog_session", Key: otherKey, TTL: time.Hour, FingerprintHeaders: []string{"User-Agent"}},
		store, nil, 60*time.Second, "hog:sess:")
	if err != nil {
		t.Fatal(err)
	}
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	rq.AddCookie(cookies[0])
	if _, err := m2.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("different-key read want ErrInvalidSession, got %v", err)
	}
}

func TestStateManagerWrongAADRejected(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil).(*stateManager)

	rec := storedSession{
		Session:      Session{Subject: "u", Expiry: time.Now().Add(time.Hour)},
		AccessExpiry: time.Now().Add(time.Hour),
	}
	plain, err := json.Marshal(&rec)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := m.sealer.seal(plain, sessionAAD) // wrong domain (cookie AAD, not stateAAD)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "abc"
	if err := store.Set(context.Background(), m.key(sessionID), []byte(wrong), time.Hour); err != nil {
		t.Fatal(err)
	}

	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(&http.Cookie{Name: "hog_session", Value: sessionID})
	if _, err := m.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("wrong-AAD record want ErrInvalidSession, got %v", err)
	}
}

type failStore struct{}

var errStore = errors.New("store down")

func (failStore) Get(_ context.Context, _ string) ([]byte, error)                  { return nil, errStore }
func (failStore) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error { return nil }
func (failStore) Delete(_ context.Context, _ string) error                         { return nil }

func TestStateManagerFailClosed(t *testing.T) {
	m := stateMgr(t, failStore{}, nil)
	rq := httptest.NewRequest("GET", "/", nil)
	rq.AddCookie(&http.Cookie{Name: "hog_session", Value: "x"})
	if _, err := m.Read(rq); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("store down want ErrInvalidSession, got %v", err)
	}
}

// fakeRefresher is a one-method Refresher double.
type fakeRefresher struct {
	tok *idp.Tokens
	err error
	n   int
}

func (f *fakeRefresher) Refresh(_ context.Context, _ string) (*idp.Tokens, error) {
	f.n++
	return f.tok, f.err
}

// issueExpiringAT issues a session whose ACCESS token is already within the skew
// window (so the next Read triggers refresh).
func issueExpiringAT(t *testing.T, m Manager) []*http.Cookie {
	t.Helper()
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	idt := &idp.Identity{Subject: "u-1"}
	tok := &idp.Tokens{AccessToken: "old-at", RefreshToken: "rt-1", Expiry: time.Now().Add(5 * time.Second)} // within 60s skew
	if err := m.Issue(wr, rq, idt, nil, tok); err != nil {
		t.Fatal(err)
	}
	return wr.Result().Cookies()
}

func readWithCookie(t *testing.T, m Manager, c *http.Cookie) (*Session, error) {
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	rq.AddCookie(c)
	return m.Read(rq)
}

func newStateMgrWithRefresher(t *testing.T, store StateStore, f Refresher) Manager {
	t.Helper()
	cfg := Config{CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour, FingerprintHeaders: []string{"User-Agent"}}
	m, err := NewStateManager(cfg, store, f, 60*time.Second, "hog:sess:")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStateManagerSilentRefreshSuccess(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{tok: &idp.Tokens{AccessToken: "new-at", RefreshToken: "rt-2", Expiry: time.Now().Add(time.Hour)}}
	m := newStateMgrWithRefresher(t, store, f)
	cookies := issueExpiringAT(t, m)

	got, err := readWithCookie(t, m, cookies[0])
	if err != nil {
		t.Fatal(err)
	}
	if f.n != 1 {
		t.Fatalf("refresh called %d times, want 1", f.n)
	}
	if got.AccessToken != "new-at" {
		t.Fatalf("access token = %q, want new-at", got.AccessToken)
	}
	// a second read no longer refreshes (new AT is an hour out)
	if _, err := readWithCookie(t, m, cookies[0]); err != nil {
		t.Fatal(err)
	}
	if f.n != 1 {
		t.Fatalf("refresh called again: %d", f.n)
	}
}

func TestStateManagerRefreshFailureWhileValid(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{err: errors.New("idp down")}
	m := newStateMgrWithRefresher(t, store, f)
	cookies := issueExpiringAT(t, m) // AT valid for ~5s

	got, err := readWithCookie(t, m, cookies[0])
	if err != nil {
		t.Fatalf("best-effort: AT still valid, want no error, got %v", err)
	}
	if got.AccessToken != "old-at" {
		t.Fatalf("want current token, got %q", got.AccessToken)
	}
}

func TestStateManagerRefreshFailureWhileExpired(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{err: errors.New("idp down")}
	cfg := Config{CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour, FingerprintHeaders: []string{"User-Agent"}}
	m, err := NewStateManager(cfg, store, f, 60*time.Second, "hog:sess:")
	if err != nil {
		t.Fatal(err)
	}
	// issue with an already-EXPIRED access token (AccessExpiry in the past)
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	if err := m.Issue(wr, rq, &idp.Identity{Subject: "u"}, nil, &idp.Tokens{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := readWithCookie(t, m, wr.Result().Cookies()[0]); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("AT expired + refresh fail want ErrInvalidSession, got %v", err)
	}
}

func TestStateManagerSilentRefreshSingleFlight(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{tok: &idp.Tokens{AccessToken: "new-at", RefreshToken: "rt-2", Expiry: time.Now().Add(time.Hour)}}
	m := newStateMgrWithRefresher(t, store, f)
	cookies := issueExpiringAT(t, m)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := readWithCookie(t, m, cookies[0])
			if err != nil {
				t.Errorf("concurrent read err: %v", err)
				return
			}
			if got.AccessToken != "new-at" {
				t.Errorf("concurrent read AT = %q, want new-at", got.AccessToken)
			}
		}()
	}
	wg.Wait()
	if f.n != 1 {
		t.Fatalf("single-flight: refresh called %d times, want 1", f.n)
	}
}

func TestStateManagerRefreshNilTokenWhileExpired(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{} // returns (nil, nil)
	cfg := Config{CookieName: "hog_session", Key: []byte(key32), TTL: time.Hour, FingerprintHeaders: []string{"User-Agent"}}
	m, err := NewStateManager(cfg, store, f, 60*time.Second, "hog:sess:")
	if err != nil {
		t.Fatal(err)
	}
	// access token already expired ⇒ a nil-token refresh must fail the session (not panic)
	wr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "/", nil)
	rq.Header.Set("User-Agent", "UA")
	if err := m.Issue(wr, rq, &idp.Identity{Subject: "u"}, nil, &idp.Tokens{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := readWithCookie(t, m, wr.Result().Cookies()[0]); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("nil-token refresh while expired want ErrInvalidSession, got %v", err)
	}
}

func TestStateManagerRefreshNilTokenWhileValid(t *testing.T) {
	store := newMemStore()
	f := &fakeRefresher{} // returns (nil, nil)
	m := newStateMgrWithRefresher(t, store, f)
	cookies := issueExpiringAT(t, m) // AT valid ~5s, within skew

	got, err := readWithCookie(t, m, cookies[0])
	if err != nil {
		t.Fatalf("nil-token while AT valid should be best-effort, got %v", err)
	}
	if got.AccessToken != "old-at" {
		t.Fatalf("want current token, got %q", got.AccessToken)
	}
}

// TestStateManagerCorrelationID verifies the stateful Read path surfaces a
// session correlation id on the Principal that is (a) non-empty, (b) stable
// across repeated reads of the same session, and (c) NEVER the raw cookie
// value — the raw stateful session id is a bearer credential and must never
// be exposed to logs.
func TestStateManagerCorrelationID(t *testing.T) {
	store := newMemStore()
	m := stateMgr(t, store, nil)
	cookies := issueState(t, m)
	rawSessionID := cookies[0].Value

	got1, err := readWithCookie(t, m, cookies[0])
	if err != nil {
		t.Fatal(err)
	}
	p1 := got1.Principal()
	if p1.SessionID == "" {
		t.Fatal("Principal.SessionID must be non-empty in stateful mode")
	}
	if p1.SessionID == rawSessionID {
		t.Fatal("Principal.SessionID must NEVER equal the raw session cookie value")
	}

	got2, err := readWithCookie(t, m, cookies[0])
	if err != nil {
		t.Fatal(err)
	}
	p2 := got2.Principal()
	if p2.SessionID != p1.SessionID {
		t.Fatalf("Principal.SessionID not stable across reads: %q != %q", p1.SessionID, p2.SessionID)
	}
}

func TestStateManagerRefreshNonRotatingKeepsOldRT(t *testing.T) {
	store := newMemStore()
	// IdP returns NO new refresh token (non-rotating, RFC 6749 §6)
	f := &fakeRefresher{tok: &idp.Tokens{AccessToken: "new-at", RefreshToken: "", Expiry: time.Now().Add(time.Hour)}}
	sm := newStateMgrWithRefresher(t, store, f).(*stateManager)
	cookies := issueExpiringAT(t, sm) // original RT = "rt-1"

	if _, err := readWithCookie(t, sm, cookies[0]); err != nil {
		t.Fatal(err)
	}
	if f.n != 1 {
		t.Fatalf("refresh n=%d, want 1", f.n)
	}
	rec, err := sm.load(context.Background(), sm.key(cookies[0].Value))
	if err != nil {
		t.Fatal(err)
	}
	if rec.RefreshToken != "rt-1" {
		t.Fatalf("non-rotating: stored RT = %q, want rt-1 (old retained)", rec.RefreshToken)
	}
	if rec.Session.AccessToken != "new-at" {
		t.Fatalf("AT not updated: %q", rec.Session.AccessToken)
	}
	// refresh must NOT extend the session past its absolute TTL (1h from issue)
	if time.Until(rec.Session.Expiry) > time.Hour {
		t.Fatalf("session extended beyond absolute TTL: %v remaining", time.Until(rec.Session.Expiry))
	}
}
