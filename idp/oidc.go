package idp

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// oidcConfig is the decoded `kind: IdP` spec.
type oidcConfig struct {
	Type         string   `yaml:"type"`
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"clientID"`
	ClientSecret string   `yaml:"clientSecret"`
	RedirectURL  string   `yaml:"redirectURL"`
	Scopes       []string `yaml:"scopes"`
}

type oidcIdP struct {
	oauth2     *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	endSession string
}

func (o *oidcIdP) AuthCodeURL(state, nonce, codeVerifier string) string {
	return o.oauth2.AuthCodeURL(state,
		oauth2.AccessTypeOffline, // request a refresh_token
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(codeVerifier),
	)
}

func (o *oidcIdP) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Tokens, *Identity, error) {
	if nonce == "" {
		return nil, nil, errors.New("oidc: nonce must not be empty")
	}
	tok, err := o.oauth2.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return nil, nil, fmt.Errorf("oidc: code exchange: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, nil, errors.New("oidc: token response had no id_token")
	}
	idt, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return nil, nil, errors.New("oidc: id_token nonce mismatch")
	}
	id, err := identityFrom(idt)
	if err != nil {
		return nil, nil, err
	}
	return &Tokens{IDToken: rawID, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, id, nil
}

func identityFrom(idt *oidc.IDToken) (*Identity, error) {
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	email, _ := raw["email"].(string)
	name, _ := raw["name"].(string)
	return &Identity{Subject: idt.Subject, Email: email, Name: name, Claims: raw}, nil
}

func (o *oidcIdP) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	src := o.oauth2.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("oidc: refresh: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID != "" {
		if _, err := o.verifier.Verify(ctx, rawID); err != nil {
			return nil, fmt.Errorf("oidc: verify refreshed id_token: %w", err)
		}
	}
	return &Tokens{IDToken: rawID, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

func (o *oidcIdP) Verify(ctx context.Context, rawJWT string) (*Identity, error) {
	idt, err := o.verifier.Verify(ctx, rawJWT)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify: %w", err)
	}
	return identityFrom(idt)
}

func (o *oidcIdP) LogoutURL(idTokenHint, postLogoutRedirect string) (string, bool) {
	if o.endSession == "" {
		return "", false
	}
	u, err := url.Parse(o.endSession)
	if err != nil {
		return "", false
	}
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	if postLogoutRedirect != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	u.RawQuery = q.Encode()
	return u.String(), true
}

// newOIDC performs eager discovery (fail-fast) and builds the connector.
func newOIDC(ctx context.Context, cfg oidcConfig) (IdP, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, fmt.Errorf("oidc: issuer, clientID, clientSecret and redirectURL are required")
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery for %q: %w", cfg.Issuer, err)
	}
	var disco struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = provider.Claims(&disco) // end_session is optional
	return &oidcIdP{
		oauth2: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		verifier:   provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		endSession: disco.EndSession,
	}, nil
}
