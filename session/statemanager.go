package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/paulopiriquito/hog/idp"
)

// stateAAD domain-separates the at-rest server-side record from the stateless
// session cookie (hog/session/v1) and the login cookie (hog/login/v1).
var stateAAD = []byte("hog/store/v1")

// storedSession is the server-side record, sealed at rest. It extends the cookie
// Session with the refresh token and the access token's own expiry.
type storedSession struct {
	Session      Session   `json:"s"`
	RefreshToken string    `json:"rt"`
	AccessExpiry time.Time `json:"ae"`
}

// stateManager keeps the session server-side: the cookie holds only an opaque ID,
// the record is sealed before it reaches the StateStore, and the access token is
// silently refreshed (the refresh logic is wired in the silent-refresh task).
type stateManager struct {
	cfg         Config
	sealer      *sealer
	store       StateStore
	provider    Refresher // nil ⇒ no silent refresh
	refreshSkew time.Duration
	keyPrefix   string
	refresh     keyedMutex
	log         *slog.Logger
}

// NewStateManager builds the server-side Manager (fail-fast on a bad key).
func NewStateManager(cfg Config, store StateStore, provider Refresher, refreshSkew time.Duration, keyPrefix string) (Manager, error) {
	s, err := newSealer32(cfg.Key)
	if err != nil {
		return nil, err
	}
	if keyPrefix == "" {
		keyPrefix = "hog:sess:"
	}
	if refreshSkew <= 0 {
		refreshSkew = 60 * time.Second
	}
	return &stateManager{
		cfg:         cfg,
		sealer:      s,
		store:       store,
		provider:    provider,
		refreshSkew: refreshSkew,
		keyPrefix:   keyPrefix,
		log:         slog.Default(),
	}, nil
}

func (m *stateManager) key(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return m.keyPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

func (m *stateManager) seal(rec *storedSession) ([]byte, error) {
	plain, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	value, err := m.sealer.seal(plain, stateAAD)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (m *stateManager) load(ctx context.Context, key string) (*storedSession, error) {
	data, err := m.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	plain, err := m.sealer.open(string(data), stateAAD)
	if err != nil {
		return nil, err
	}
	var rec storedSession
	if err := json.Unmarshal(plain, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *stateManager) Issue(w http.ResponseWriter, r *http.Request, idt *idp.Identity, userinfo map[string]any, tok *idp.Tokens) error {
	rec := storedSession{
		Session:      makeSession(m.cfg, idt, userinfo, tok, r),
		RefreshToken: tok.RefreshToken,
		AccessExpiry: tok.Expiry,
	}
	data, err := m.seal(&rec)
	if err != nil {
		return err
	}
	var idBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return err
	}
	sessionID := base64.RawURLEncoding.EncodeToString(idBytes[:])
	if err := m.store.Set(r.Context(), m.key(sessionID), data, m.cfg.TTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureFromRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.cfg.TTL.Seconds()),
	})
	return nil
}

func (m *stateManager) Read(r *http.Request) (*Session, error) {
	c, err := r.Cookie(m.cfg.CookieName)
	if err != nil || c.Value == "" {
		return nil, ErrNoSession
	}
	key := m.key(c.Value)
	rec, err := m.load(r.Context(), key)
	if err != nil {
		if !errors.Is(err, ErrStateNotFound) {
			m.log.Warn("session: state read failed (fail-closed)", "err", err)
		}
		return nil, ErrInvalidSession
	}
	if rec.Session.Expired() {
		return nil, ErrInvalidSession
	}
	if !fingerprintEqual(rec.Session.Fingerprint, computeFingerprint(m.cfg.FingerprintHeaders, r)) {
		return nil, ErrInvalidSession
	}
	rec = m.maybeRefresh(r.Context(), key, c.Value, rec)
	if rec == nil {
		return nil, ErrInvalidSession
	}
	rec.Session.CorrelationID = correlationID(c.Value)
	return &rec.Session, nil
}

// correlationID is a short, non-reversible handle for a stateful session id, safe
// to log. The raw session id is a bearer credential and is NEVER logged.
func correlationID(sessionID string) string {
	sum := sha256.Sum256([]byte("hog/sesscorr/v1:" + sessionID))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:12]
}

// maybeRefresh silently refreshes the access token when it is within refreshSkew
// of expiry. Returns the (possibly refreshed) record, or nil when the access token
// is already expired and refresh failed (⇒ the caller returns ErrInvalidSession).
// Concurrent refreshes for one session collapse via an in-process per-id lock;
// cross-instance double-refresh is tolerated (the loser uses its still-valid token).
// The in-process single-flight assumes store.Set is synchronously durable before
// it returns; an async/replica-lagged store degrades to the (benign) cross-instance case.
func (m *stateManager) maybeRefresh(ctx context.Context, key, sessionID string, rec *storedSession) *storedSession {
	if m.provider == nil || rec.RefreshToken == "" {
		return rec
	}
	if !time.Now().After(rec.AccessExpiry.Add(-m.refreshSkew)) {
		return rec // access token not near expiry
	}
	unlock := m.refresh.lock(sessionID)
	defer unlock()

	// double-checked: another goroutine may have refreshed while we waited.
	if fresh, lerr := m.load(ctx, key); lerr == nil && time.Now().Before(fresh.AccessExpiry.Add(-m.refreshSkew)) {
		return fresh
	}

	// The rotation + re-store must not be aborted by a client disconnect: a
	// cancelled rotation can strand the session (RT rotated at the IdP, the new
	// RT never stored). Detach from request cancellation and bound with a timeout.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	tok, err := m.provider.Refresh(rctx, rec.RefreshToken)
	if err != nil || tok == nil {
		if time.Now().Before(rec.AccessExpiry) {
			m.log.Warn("session: silent refresh failed; access token still valid", "err", err) // never log the token
			return rec
		}
		return nil // access token already dead ⇒ session can't function
	}
	rec.Session.AccessToken = tok.AccessToken
	rec.AccessExpiry = tok.Expiry
	if tok.RefreshToken != "" {
		rec.RefreshToken = tok.RefreshToken // rotation
	}
	if remaining := time.Until(rec.Session.Expiry); remaining > 0 {
		if data, serr := m.seal(rec); serr != nil {
			m.log.Warn("session: seal after refresh failed", "err", serr)
		} else if serr := m.store.Set(rctx, key, data, remaining); serr != nil {
			m.log.Warn("session: re-store after refresh failed", "err", serr)
		}
	}
	return rec
}

func (m *stateManager) Clear(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(m.cfg.CookieName); err == nil && c.Value != "" {
		if derr := m.store.Delete(r.Context(), m.key(c.Value)); derr != nil {
			m.log.Warn("session: state delete failed", "err", derr)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureFromRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
