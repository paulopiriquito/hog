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

// IdP is the external identity-provider connector.
type IdP interface {
	AuthCodeURL(state, nonce, codeVerifier string) string
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Tokens, *Identity, error)
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	Verify(ctx context.Context, rawJWT string) (*Identity, error)
	LogoutURL(idTokenHint, postLogoutRedirect string) (string, bool)
}
