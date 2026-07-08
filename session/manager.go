package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/paulopiriquito/hog/idp"
)

// Sentinel errors from Read.
var (
	ErrNoSession      = errors.New("session: no session cookie")
	ErrInvalidSession = errors.New("session: invalid session")
)

// sessionAAD domain-separates the session cookie from other sealed cookies
// (e.g. the auth package's login cookie) that share the same key.
var sessionAAD = []byte("hog/session/v1")

// Manager builds, seals, reads, and clears sessions. The cookie implementation
// is stateless; a Valkey-backed implementation (same interface) arrives in #5.
type Manager interface {
	New(idt *idp.Identity, userinfo map[string]any, tok *idp.Tokens, r *http.Request) *Session
	Write(w http.ResponseWriter, r *http.Request, s *Session) error
	Read(r *http.Request) (*Session, error)
	Clear(w http.ResponseWriter, r *http.Request)
}

type cookieManager struct {
	cfg    Config
	sealer *sealer
}

// NewManager builds the cookie-backed Manager (fail-fast on a bad key).
func NewManager(cfg Config) (Manager, error) {
	s, err := newSealer32(cfg.Key)
	if err != nil {
		return nil, err
	}
	return &cookieManager{cfg: cfg, sealer: s}, nil
}

func (m *cookieManager) New(idt *idp.Identity, userinfo map[string]any, tok *idp.Tokens, r *http.Request) *Session {
	now := time.Now()
	return &Session{
		Subject:     idt.Subject,
		Passport:    projectPassport(m.cfg.PassportClaims, idt.Claims, userinfo),
		Groups:      projectGroups(m.cfg.Groups, userinfo),
		AccessToken: tok.AccessToken, // tok.RefreshToken intentionally discarded (server-side-only, #5)
		Expiry:      now.Add(m.cfg.TTL),
		IssuedAt:    now,
		Fingerprint: computeFingerprint(m.cfg.FingerprintHeaders, r),
	}
}

func (m *cookieManager) Write(w http.ResponseWriter, r *http.Request, s *Session) error {
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	value, err := m.sealer.seal(payload, sessionAAD)
	if err != nil {
		return err
	}
	secure := secureFromRequest(r)
	clearChunkedCookie(w, r, m.cfg.CookieName, secure) // drop stale chunks from a larger prior session
	writeChunkedCookie(w, m.cfg.CookieName, value, secure)
	return nil
}

func (m *cookieManager) Read(r *http.Request) (*Session, error) {
	value, ok := readChunkedCookie(r, m.cfg.CookieName)
	if !ok {
		return nil, ErrNoSession
	}
	payload, err := m.sealer.open(value, sessionAAD)
	if err != nil {
		return nil, ErrInvalidSession
	}
	var s Session
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, ErrInvalidSession
	}
	if s.Expired() {
		return nil, ErrInvalidSession
	}
	if !fingerprintEqual(s.Fingerprint, computeFingerprint(m.cfg.FingerprintHeaders, r)) {
		return nil, ErrInvalidSession
	}
	return &s, nil
}

func (m *cookieManager) Clear(w http.ResponseWriter, r *http.Request) {
	clearChunkedCookie(w, r, m.cfg.CookieName, secureFromRequest(r))
}

// secureFromRequest reports whether the external scheme is https. HOG sits behind
// a TLS-terminating LB, so trust X-Forwarded-Proto; default secure when unknown.
func secureFromRequest(r *http.Request) bool {
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return p == "https"
	}
	return true // default secure: HOG runs behind a TLS-terminating LB
}
