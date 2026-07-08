package session

import "context"

// Principal is the authenticated user as seen by downstream middlewares, the
// backend-forwarding proxy, the authz gate, and plugins. It carries identity +
// the access token (for backend calls) but NEVER the refresh token or fingerprint.
type Principal struct {
	Subject     string
	Passport    map[string]any
	Groups      []string
	AccessToken string
}

// Principal derives the request-context view from a full session.
func (s *Session) Principal() *Principal {
	return &Principal{
		Subject:     s.Subject,
		Passport:    s.Passport,
		Groups:      s.Groups,
		AccessToken: s.AccessToken,
	}
}

type principalKey struct{}

// WithPrincipal returns a child context carrying p (used by #3's session-resolve gate).
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the authenticated Principal, or (nil,false) if absent.
// A stored nil pointer is treated as absent (defense-in-depth against
// WithPrincipal(ctx, nil) inadvertently authenticating a request).
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

// InGroup reports whether the request's principal belongs to group g.
func InGroup(ctx context.Context, g string) bool {
	if p, ok := FromContext(ctx); ok {
		for _, x := range p.Groups {
			if x == g {
				return true
			}
		}
	}
	return false
}

// Claim returns a passport claim by name.
func Claim(ctx context.Context, name string) (any, bool) {
	if p, ok := FromContext(ctx); ok {
		v, ok := p.Passport[name]
		return v, ok
	}
	return nil, false
}
