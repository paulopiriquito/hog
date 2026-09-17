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
	Type           string        `yaml:"type"`
	Issuer         string        `yaml:"issuer"`
	ClientID       string        `yaml:"clientID"`
	ClientSecret   string        `yaml:"clientSecret"`
	RedirectURL    string        `yaml:"redirectURL"`
	BearerAudience string        `yaml:"bearerAudience"`
	Bearer         *bearerConfig `yaml:"bearer"`
	Scopes         []string      `yaml:"scopes"`
	PKCE           *bool         `yaml:"pkce"`
}

// bearerConfig tunes how Authorization: Bearer access tokens are verified. Some
// providers sign access tokens with a key set that is not the ID-token
// jwks_uri and issue them without aud or sub.
type bearerConfig struct {
	JWKSURL         string   `yaml:"jwksURL"`         // key set for access tokens; default: discovery jwks_access_token_uri, else jwks_uri
	AudienceClaim   string   `yaml:"audienceClaim"`   // claim compared with bearerAudience instead of aud (e.g. client_id)
	RequireAudience *bool    `yaml:"requireAudience"` // default true; false accepts tokens with no audience at all
	SigningAlgs     []string `yaml:"signingAlgs"`     // algorithms accepted for access tokens; defaults to the provider's advertised ID-token algorithms
}

type oidcIdP struct {
	oauth2           *oauth2.Config
	verifier         *oidc.IDTokenVerifier
	bearerVerifier   *oidc.IDTokenVerifier
	bearerAud        string
	bearerAudClaim   string
	bearerRequireAud bool
	endSession       string
	provider         *oidc.Provider
	pkce             bool
}

func (o *oidcIdP) AuthCodeURL(state, nonce, codeVerifier string) string {
	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oidc.Nonce(nonce)}
	if o.pkce {
		opts = append(opts, oauth2.S256ChallengeOption(codeVerifier))
	}
	return o.oauth2.AuthCodeURL(state, opts...)
}

func (o *oidcIdP) UsesPKCE() bool { return o.pkce }

func (o *oidcIdP) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Tokens, *Identity, error) {
	if nonce == "" {
		return nil, nil, errors.New("oidc: nonce must not be empty")
	}
	var exchOpts []oauth2.AuthCodeOption
	if o.pkce {
		exchOpts = append(exchOpts, oauth2.VerifierOption(codeVerifier))
	}
	tok, err := o.oauth2.Exchange(ctx, code, exchOpts...)
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

// VerifyAccessToken verifies a Bearer access-token JWT: signature against the
// access-token key set (bearer.jwksURL, else discovery's jwks_access_token_uri,
// else the ID-token JWKS), issuer and expiry; then the audience: the standard
// aud (default) or bearer.audienceClaim (e.g. client_id) must equal
// bearerAudience (the client ID by default), unless bearer.requireAudience is
// false. A token without sub is accepted — the session layer resolves the
// subject from identity.subjectClaim.
func (o *oidcIdP) VerifyAccessToken(ctx context.Context, rawJWT string) (*Identity, error) {
	idt, err := o.bearerVerifier.Verify(ctx, rawJWT)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify access token: %w", err)
	}
	id, err := identityFrom(idt)
	if err != nil {
		return nil, err
	}
	if o.bearerRequireAud && o.bearerAudClaim != "" {
		if !claimHas(id.Claims[o.bearerAudClaim], o.bearerAud) {
			return nil, fmt.Errorf("oidc: verify access token: %s does not match the expected audience", o.bearerAudClaim)
		}
	}
	return id, nil
}

// claimHas reports whether a string or string-array claim contains want.
func claimHas(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range t {
			if s == want {
				return true
			}
		}
	}
	return false
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

func (o *oidcIdP) UserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	ui, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken}))
	if err != nil {
		return nil, fmt.Errorf("oidc: userinfo: %w", err)
	}
	var claims map[string]any
	if err := ui.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: parse userinfo claims: %w", err)
	}
	return claims, nil
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
	pkce := true
	if cfg.PKCE != nil {
		pkce = *cfg.PKCE
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery for %q: %w", cfg.Issuer, err)
	}
	var disco struct {
		EndSession      string   `json:"end_session_endpoint"`
		AccessTokenJWKS string   `json:"jwks_access_token_uri"` // when advertised: access tokens use their own key set
		SigningAlgs     []string `json:"id_token_signing_alg_values_supported"`
	}
	_ = provider.Claims(&disco) // end_session, jwks_access_token_uri and the signing-alg list are all optional
	bearerAud := cfg.BearerAudience
	if bearerAud == "" {
		bearerAud = cfg.ClientID
	}
	requireAud := true
	audClaim := ""
	jwksURL := disco.AccessTokenJWKS
	var signingAlgs []string
	if cfg.Bearer != nil {
		if cfg.Bearer.RequireAudience != nil {
			requireAud = *cfg.Bearer.RequireAudience
		}
		audClaim = cfg.Bearer.AudienceClaim
		if cfg.Bearer.JWKSURL != "" {
			jwksURL = cfg.Bearer.JWKSURL
		}
		signingAlgs = cfg.Bearer.SigningAlgs
	}
	if !requireAud && audClaim != "" {
		return nil, fmt.Errorf("oidc: bearer.requireAudience false cannot be combined with bearer.audienceClaim (the claim would never be checked)")
	}
	if len(signingAlgs) == 0 {
		signingAlgs = disco.SigningAlgs
	}
	// The standard aud check stays inside go-oidc unless the token carries no aud
	// (requireAudience false) or the audience lives in another claim (checked after Verify).
	bearerCfg := &oidc.Config{ClientID: bearerAud}
	if !requireAud || audClaim != "" {
		bearerCfg = &oidc.Config{SkipClientIDCheck: true}
	}
	// Set on both paths. A verifier built against a dedicated key set never goes
	// through provider.Verifier, which is what would otherwise copy the discovery
	// document's id_token_signing_alg_values_supported in, and go-oidc would then
	// fall back to RS256 only. On the provider.Verifier path an explicit
	// bearer.signingAlgs must win over the advertised list, which it does not if
	// the field is left empty here.
	if len(signingAlgs) > 0 {
		bearerCfg.SupportedSigningAlgs = signingAlgs
	}
	var bearerVerifier *oidc.IDTokenVerifier
	if jwksURL != "" {
		bearerVerifier = oidc.NewVerifier(cfg.Issuer, oidc.NewRemoteKeySet(ctx, jwksURL), bearerCfg)
	} else {
		bearerVerifier = provider.Verifier(bearerCfg)
	}
	return &oidcIdP{
		oauth2: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		verifier:         provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		bearerVerifier:   bearerVerifier,
		bearerAud:        bearerAud,
		bearerAudClaim:   audClaim,
		bearerRequireAud: requireAud,
		endSession:       disco.EndSession,
		provider:         provider,
		pkce:             pkce,
	}, nil
}
