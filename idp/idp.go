// Package idp is HOG's connector to an external OpenID Connect provider (the
// relying-party side of the BFF). It runs the OIDC protocol so application and
// service developers configure issuer/client-id/client-secret/redirect and
// write no auth-flow code themselves.
package idp

import (
	"context"
	"time"
)

// Tokens is the set of tokens returned by the provider.
type Tokens struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// Identity is the verified passport extracted from a token.
type Identity struct {
	Subject string         // sub
	Email   string         // email claim, if present
	Name    string         // name claim, if present
	Claims  map[string]any // full claim set, for downstream projection (#3)
}

// VerificationOnly is the optional interface a connector implements when it can
// only verify tokens another party issued — it holds no client secret and no
// redirect URL, so it can never start a login flow. It is deliberately separate
// from IdP: a connector that does not implement it is a full connector, and no
// existing implementation has to change.
//
// IsVerificationOnly is the safe way to ask.
type VerificationOnly interface {
	// VerificationOnly reports whether the connector is verification-only.
	VerificationOnly() bool
}

// IsVerificationOnly reports whether p is a verification-only connector: one
// that verifies tokens but cannot run the authorization-code flow. A nil p, or
// one that does not implement VerificationOnly, is not.
func IsVerificationOnly(p IdP) bool {
	v, ok := p.(VerificationOnly)
	return ok && v.VerificationOnly()
}

// IdP is the external identity-provider connector.
type IdP interface {
	AuthCodeURL(state, nonce, codeVerifier string) string
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Tokens, *Identity, error)
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	Verify(ctx context.Context, rawJWT string) (*Identity, error)
	VerifyAccessToken(ctx context.Context, rawJWT string) (*Identity, error)
	LogoutURL(idTokenHint, postLogoutRedirect string) (string, bool)
	UserInfo(ctx context.Context, accessToken string) (map[string]any, error)
	UsesPKCE() bool
}
