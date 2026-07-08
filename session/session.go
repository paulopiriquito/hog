package session

import "time"

// Session is the stateless session payload sealed into the cookie. It holds the
// access token (the BFF injects it into backend calls) but NEVER the refresh
// token or id_token (refresh is server-side-only; see spec).
type Session struct {
	Subject     string         `json:"sub"`
	Passport    map[string]any `json:"passport,omitempty"`
	Groups      []string       `json:"groups,omitempty"`
	AccessToken string         `json:"at"`
	Expiry      time.Time      `json:"exp"`
	IssuedAt    time.Time      `json:"iat"`
	Fingerprint string         `json:"fp"`

	// CorrelationID is a non-persisted, non-reversible handle for a stateful
	// session, set on Read in stateful mode and safe to log. It is NEVER the
	// raw session id (a bearer credential), and json:"-" so it is never sealed
	// or stored.
	CorrelationID string `json:"-"`
}

// Expired reports whether the session is past its expiry.
func (s *Session) Expired() bool { return time.Now().After(s.Expiry) }

// PublicView is the SPA-facing projection: identity + TTL, never tokens or the
// fingerprint.
type PublicView struct {
	Subject   string         `json:"subject"`
	Passport  map[string]any `json:"passport,omitempty"`
	Groups    []string       `json:"groups,omitempty"`
	IssuedAt  time.Time      `json:"issuedAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
	ExpiresIn int            `json:"expiresIn"`
}

// PublicView returns the SPA-safe view of the session.
func (s *Session) PublicView() PublicView {
	return PublicView{
		Subject:   s.Subject,
		Passport:  s.Passport,
		Groups:    s.Groups,
		IssuedAt:  s.IssuedAt,
		ExpiresAt: s.Expiry,
		ExpiresIn: int(time.Until(s.Expiry).Seconds()),
	}
}
