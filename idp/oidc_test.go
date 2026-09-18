package idp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewOIDCBuildsAgainstProvider(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	_, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1",
		ClientSecret: "secret", RedirectURL: "https://app/cb",
	})
	if err != nil {
		t.Fatalf("newOIDC: %v", err)
	}
}

func TestNewOIDCRequiresFields(t *testing.T) {
	_, err := newOIDC(context.Background(), oidcConfig{Type: "oidc", Issuer: "https://x"})
	if err == nil {
		t.Fatal("want error for missing clientID/secret/redirect")
	}
}

func TestNewOIDCDiscoveryFailsFast(t *testing.T) {
	_, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: "http://127.0.0.1:1/nope", ClientID: "c",
		ClientSecret: "s", RedirectURL: "https://app/cb",
	})
	if err == nil {
		t.Fatal("want discovery error for unreachable issuer")
	}
}

func TestAuthCodeURL(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1",
		ClientSecret: "secret", RedirectURL: "https://app/cb", Scopes: []string{"openid", "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := p.AuthCodeURL("state-xyz", "nonce-abc", "verifier-123456789012345678901234567890123456")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "client-1" || q.Get("redirect_uri") != "https://app/cb" {
		t.Fatalf("client/redirect = %q %q", q.Get("client_id"), q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-xyz" || q.Get("nonce") != "nonce-abc" {
		t.Fatalf("state/nonce = %q %q", q.Get("state"), q.Get("nonce"))
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("pkce = %q %q", q.Get("code_challenge_method"), q.Get("code_challenge"))
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
}

func buildOIDC(t *testing.T, f *fakeIdP) IdP {
	t.Helper()
	p, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: f.clientID,
		ClientSecret: "secret", RedirectURL: "https://app/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExchangeReturnsTokensAndIdentity(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	f.idTokenClaims = map[string]any{"nonce": "n1", "email": "a@b.co", "name": "Alice", "sub": "u-9"}
	p := buildOIDC(t, f)

	tokens, id, err := p.Exchange(context.Background(), "any-code", "any-verifier", "n1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tokens.AccessToken != "fake-access-token" || tokens.RefreshToken != "fake-refresh-token" || tokens.IDToken == "" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if id.Subject != "u-9" || id.Email != "a@b.co" || id.Name != "Alice" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestExchangeRejectsNonceMismatch(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	f.idTokenClaims = map[string]any{"nonce": "issued-nonce"}
	p := buildOIDC(t, f)
	if _, _, err := p.Exchange(context.Background(), "c", "v", "expected-different"); err == nil {
		t.Fatal("want nonce-mismatch error")
	}
}

func TestRefresh(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	f.idTokenClaims = map[string]any{"sub": "u-1"}
	p := buildOIDC(t, f)
	tokens, err := p.Refresh(context.Background(), "fake-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.AccessToken != "fake-access-token" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestVerifyAcceptsValidRejectsBad(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)

	good := f.sign(t, map[string]any{"sub": "u-1", "email": "x@y.z"})
	id, err := p.Verify(context.Background(), good)
	if err != nil || id.Subject != "u-1" {
		t.Fatalf("valid verify: id=%+v err=%v", id, err)
	}
	if _, err := p.Verify(context.Background(), signWith(t, f.priv, "https://evil", "client-1", nil)); err == nil {
		t.Fatal("want wrong-issuer error")
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := p.Verify(context.Background(), signWith(t, other, f.srv.URL, "client-1", nil)); err == nil {
		t.Fatal("want bad-signature error")
	}
	if _, err := p.Verify(context.Background(), f.sign(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})); err == nil {
		t.Fatal("want expired error")
	}
	if _, err := p.Verify(context.Background(), signWith(t, f.priv, f.srv.URL, "wrong-aud", nil)); err == nil {
		t.Fatal("want wrong-audience error")
	}
}

func TestLogoutURL(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)
	u, ok := p.LogoutURL("the-id-token", "https://app/done")
	if !ok {
		t.Fatal("want logout URL (fake advertises end_session_endpoint)")
	}
	if !strings.Contains(u, "id_token_hint=the-id-token") || !strings.Contains(u, "post_logout_redirect_uri=") {
		t.Fatalf("logout url = %q", u)
	}
}

func TestExchangeRejectsEmptyNonce(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)
	if _, _, err := p.Exchange(context.Background(), "c", "v", ""); err == nil {
		t.Fatal("want error for empty nonce")
	}
}

func TestLogoutURLAbsent(t *testing.T) {
	o := &oidcIdP{endSession: ""}
	if _, ok := o.LogoutURL("a", "b"); ok {
		t.Fatal("want false when end_session_endpoint is absent")
	}
}

func TestVerifyAcceptsArrayAudience(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)
	// Real IdPs (e.g. Azure) emit aud as an array; clientID present in it must verify.
	tok := signWith(t, f.priv, f.srv.URL, "ignored", map[string]any{"aud": []any{"other", "client-1"}})
	if _, err := p.Verify(context.Background(), tok); err != nil {
		t.Fatalf("array aud containing clientID should verify: %v", err)
	}
}

func TestUserInfo(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)
	claims, err := p.UserInfo(context.Background(), "any-access-token")
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if claims["email"] != "ui@example.com" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestPKCEOptional(t *testing.T) {
	f := newFakeIdP(t, "client-1")

	// default (unset) ⇒ PKCE on
	on := buildOIDC(t, f)
	if !on.UsesPKCE() {
		t.Fatal("default should use PKCE")
	}
	u, _ := url.Parse(on.AuthCodeURL("st", "no", "verifier-1234567890123456789012345678901234567890"))
	if u.Query().Get("code_challenge") == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE on: missing challenge: %s", u.RawQuery)
	}

	// pkce: false ⇒ no challenge
	no, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: f.clientID, ClientSecret: "s",
		RedirectURL: "https://app/cb", PKCE: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if no.UsesPKCE() {
		t.Fatal("pkce:false should not use PKCE")
	}
	u2, _ := url.Parse(no.AuthCodeURL("st", "no", ""))
	if u2.Query().Get("code_challenge") != "" {
		t.Fatalf("PKCE off: unexpected challenge: %s", u2.RawQuery)
	}
	if u2.Query().Get("state") != "st" || u2.Query().Get("nonce") != "no" {
		t.Fatalf("state/nonce must always be present: %s", u2.RawQuery)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestVerifyAccessTokenRejections(t *testing.T) {
	f := newFakeIdPNoAccessJWKS(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s",
		RedirectURL: "https://app/cb", BearerAudience: "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// expired ⇒ rejected
	expired := signWith(t, f.priv, f.srv.URL, "api-1", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), expired); err == nil {
		t.Fatal("expired access token must be rejected")
	}

	// wrong issuer ⇒ rejected
	evilIss := signWith(t, f.priv, "https://evil", "api-1", nil)
	if _, err := oi.VerifyAccessToken(context.Background(), evilIss); err == nil {
		t.Fatal("wrong-issuer access token must be rejected")
	}

	// opaque / non-JWT ⇒ rejected cleanly (error, no panic)
	if _, err := oi.VerifyAccessToken(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("garbage token must be rejected")
	}
}

func TestVerifyAccessTokenAudience(t *testing.T) {
	f := newFakeIdPNoAccessJWKS(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s",
		RedirectURL: "https://app/cb", BearerAudience: "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// aud=api-1 (the configured bearerAudience) ⇒ accepted.
	good := signWith(t, f.priv, f.srv.URL, "api-1", map[string]any{"email": "a@b.co"})
	id, err := oi.VerifyAccessToken(context.Background(), good)
	if err != nil {
		t.Fatalf("valid access token rejected: %v", err)
	}
	if id.Subject != "user-123" {
		t.Fatalf("subject = %q", id.Subject)
	}

	// aud=client-1 (wrong audience for bearer) ⇒ rejected.
	wrong := signWith(t, f.priv, f.srv.URL, "client-1", map[string]any{})
	if _, err := oi.VerifyAccessToken(context.Background(), wrong); err == nil {
		t.Fatal("token with wrong audience must be rejected")
	}

	// bad signature ⇒ rejected.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := signWith(t, other, f.srv.URL, "api-1", map[string]any{})
	if _, err := oi.VerifyAccessToken(context.Background(), bad); err == nil {
		t.Fatal("token with bad signature must be rejected")
	}
}

func TestVerifyAccessTokenDefaultAudienceIsClientID(t *testing.T) {
	f := newFakeIdPNoAccessJWKS(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		// BearerAudience omitted ⇒ defaults to ClientID.
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := signWith(t, f.priv, f.srv.URL, "client-1", map[string]any{})
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err != nil {
		t.Fatalf("default-audience token rejected: %v", err)
	}
}

func TestVerifyAccessTokenDedicatedJWKSFromDiscovery(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{AudienceClaim: "client_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := oi.VerifyAccessToken(context.Background(), f.subjectlessAccessToken(t, "client-1", "u-10427"))
	if err != nil {
		t.Fatalf("subjectless access token rejected: %v", err)
	}
	if id.Subject != "" || id.Claims["uid"] != "u-10427" {
		t.Fatalf("identity = %+v; want empty sub and the uid claim", id)
	}
	wrongKey := signRaw(t, f.priv, "test-key", map[string]any{"iss": f.srv.URL, "client_id": "client-1", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), wrongKey); err == nil {
		t.Fatal("a token signed by the ID-token key must be rejected by the access-token key set")
	}
	if _, err := oi.VerifyAccessToken(context.Background(), f.subjectlessAccessToken(t, "client-2", "u")); err == nil {
		t.Fatal("client_id mismatch must be rejected")
	}
	expired := signRaw(t, f.accessPriv, "access-key", map[string]any{"iss": f.srv.URL, "client_id": "client-1", "exp": time.Now().Add(-time.Minute).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), expired); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestVerifyAccessTokenExplicitJWKSURLWins(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{JWKSURL: f.srv.URL + "/jwks", AudienceClaim: "client_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := signRaw(t, f.priv, "test-key", map[string]any{"iss": f.srv.URL, "client_id": "client-1", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err != nil {
		t.Fatalf("explicit jwksURL not honoured: %v", err)
	}
}

func TestVerifyAccessTokenRequireAudienceFalseAcceptsNoAudience(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{RequireAudience: boolPtr(false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := signRaw(t, f.accessPriv, "access-key", map[string]any{"iss": f.srv.URL, "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err != nil {
		t.Fatalf("no-audience token must pass when requireAudience is false: %v", err)
	}
}

// TestVerifyAccessTokenSigningAlgsExplicitOverridesDefault is the C1
// regression: the dedicated-key-set path (bearer.jwksURL / discovery's
// jwks_access_token_uri) built a verifier with no SupportedSigningAlgs, so
// go-oidc silently defaulted to RS256-only. An explicit bearer.signingAlgs
// must reach the verifier: restricting to ES256 must now reject a token
// signed with the fake's RS256 access key, proving the field is honoured.
// TestVerifyAccessTokenSigningAlgsWithoutDedicatedKeySet covers the path where
// the provider advertises no dedicated access-token key set, so the verifier is
// built by provider.Verifier. That helper fills SupportedSigningAlgs from the
// discovery document, which silently overrode an explicit bearer.signingAlgs
// until it was set before the verifier is built.
func TestVerifyAccessTokenSigningAlgsWithoutDedicatedKeySet(t *testing.T) {
	f := newFakeIdPNoAccessJWKS(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{AudienceClaim: "client_id", SigningAlgs: []string{"ES256"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := signRaw(t, f.priv, "test-key", map[string]any{
		"iss": f.srv.URL, "client_id": "client-1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err == nil {
		t.Fatal("an RS256 access token must be rejected when bearer.signingAlgs allows only ES256, with or without a dedicated key set")
	}
}

func TestVerifyAccessTokenSigningAlgsExplicitOverridesDefault(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{AudienceClaim: "client_id", SigningAlgs: []string{"ES256"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := f.subjectlessAccessToken(t, "client-1", "u-10427")
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err == nil {
		t.Fatal("RS256-signed access token must be rejected when bearer.signingAlgs is restricted to ES256")
	}
}

// TestVerifyAccessTokenSigningAlgsFromDiscovery proves the "else discovery
// list" fallback: with no bearer.signingAlgs override, the dedicated-key-set
// verifier must pick up id_token_signing_alg_values_supported from discovery.
// The fake here advertises only ES256, so its RS256-signed access token must
// be rejected even though nothing in the bearer config mentions algorithms.
func TestVerifyAccessTokenSigningAlgsFromDiscovery(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	f.signingAlgs = []string{"ES256"}
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{AudienceClaim: "client_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := f.subjectlessAccessToken(t, "client-1", "u-10427")
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err == nil {
		t.Fatal("RS256-signed access token must be rejected when discovery advertises only ES256")
	}
}

// TestNewOIDCRejectsRequireAudienceFalseWithAudienceClaim is the C2
// regression: requireAudience:false together with a set audienceClaim reads
// as the opposite of what the operator intended (the claim would never be
// checked) and must be rejected at config load.
func TestNewOIDCRejectsRequireAudienceFalseWithAudienceClaim(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	_, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{RequireAudience: boolPtr(false), AudienceClaim: "client_id"},
	})
	if err == nil {
		t.Fatal("want error combining bearer.requireAudience:false with bearer.audienceClaim")
	}
}

// TestVerifyAccessTokenNilBearerDedicatedJWKSFromDiscovery is the C3
// regression: with Bearer == nil (its zero value), discovery advertising
// jwks_access_token_uri must still be picked up automatically, and the
// standard aud check must still apply against bearerAudience (default:
// ClientID).
func TestVerifyAccessTokenNilBearerDedicatedJWKSFromDiscovery(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		// Bearer omitted (nil).
	})
	if err != nil {
		t.Fatal(err)
	}
	// Signed by the access key with aud == client-1 (the default bearerAudience): verifies.
	good := signRaw(t, f.accessPriv, "access-key", map[string]any{
		"iss": f.srv.URL, "aud": "client-1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := oi.VerifyAccessToken(context.Background(), good); err != nil {
		t.Fatalf("access-key-signed token with aud=client-1 rejected: %v", err)
	}
	// Signed by the ID-token key: rejected by the dedicated access-token key set.
	wrongKey := signRaw(t, f.priv, "test-key", map[string]any{
		"iss": f.srv.URL, "aud": "client-1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := oi.VerifyAccessToken(context.Background(), wrongKey); err == nil {
		t.Fatal("a token signed by the ID-token key must be rejected by the access-token key set")
	}
}

func TestVerifyAccessTokenWrongIssuerRejected(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	oi, err := newOIDC(context.Background(), oidcConfig{
		Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "s", RedirectURL: "https://app/cb",
		Bearer: &bearerConfig{AudienceClaim: "client_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := signRaw(t, f.accessPriv, "access-key", map[string]any{"iss": "https://evil.example", "client_id": "client-1", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := oi.VerifyAccessToken(context.Background(), tok); err == nil {
		t.Fatal("a token from another issuer must be rejected")
	}
}

// A verification-only connector is built from the two fields verification
// actually uses: the issuer (discovered, and checked on every token) and the
// client ID (the expected audience). No client secret, no redirect URL.
func TestNewOIDCVerificationOnlyNeedsOnlyIssuerAndClientID(t *testing.T) {
	f := newFakeIdPNoAccessJWKS(t, "client-1")
	p, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1", VerificationOnly: true,
	})
	if err != nil {
		t.Fatalf("newOIDC verificationOnly: %v", err)
	}
	if !IsVerificationOnly(p) {
		t.Fatal("IsVerificationOnly = false for a verificationOnly connector")
	}
	// It is built to verify, so verification still works end to end.
	id, err := p.VerifyAccessToken(context.Background(), signWith(t, f.priv, f.srv.URL, "client-1", map[string]any{"sub": "u-7"}))
	if err != nil {
		t.Fatalf("VerifyAccessToken on a verificationOnly connector: %v", err)
	}
	if id.Subject != "u-7" {
		t.Fatalf("subject = %q, want u-7", id.Subject)
	}
	if _, err := p.VerifyAccessToken(context.Background(), signWith(t, f.priv, f.srv.URL, "other-client", map[string]any{})); err == nil {
		t.Fatal("a token for another audience must still be rejected")
	}
}

// Without the declaration, all four fields stay required — each one on its own.
func TestNewOIDCWithoutVerificationOnlyRequiresAllFour(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	full := oidcConfig{Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "secret", RedirectURL: "https://app/cb"}
	if _, err := newOIDC(context.Background(), full); err != nil {
		t.Fatalf("the full four-field config must still build: %v", err)
	}
	for _, tc := range []struct {
		field string
		drop  func(c *oidcConfig)
	}{
		{"issuer", func(c *oidcConfig) { c.Issuer = "" }},
		{"clientID", func(c *oidcConfig) { c.ClientID = "" }},
		{"clientSecret", func(c *oidcConfig) { c.ClientSecret = "" }},
		{"redirectURL", func(c *oidcConfig) { c.RedirectURL = "" }},
	} {
		t.Run("without "+tc.field, func(t *testing.T) {
			c := full
			tc.drop(&c)
			if _, err := newOIDC(context.Background(), c); err == nil {
				t.Fatalf("want an error when %s is missing and verificationOnly is not set", tc.field)
			}
		})
	}
}

// Supplying a credential the connector can never spend, or a callback route it
// never serves, is the copy-paste this feature exists to prevent: refuse it.
func TestNewOIDCVerificationOnlyRejectsLoginFlowFields(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	base := oidcConfig{Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1", VerificationOnly: true}
	withSecret := base
	withSecret.ClientSecret = "secret"
	if _, err := newOIDC(context.Background(), withSecret); err == nil {
		t.Fatal("want an error for verificationOnly + clientSecret")
	}
	withRedirect := base
	withRedirect.RedirectURL = "https://app/cb"
	if _, err := newOIDC(context.Background(), withRedirect); err == nil {
		t.Fatal("want an error for verificationOnly + redirectURL")
	}
	noClientID := base
	noClientID.ClientID = ""
	if _, err := newOIDC(context.Background(), noClientID); err == nil {
		t.Fatal("want an error for verificationOnly without a clientID")
	}
	noIssuer := base
	noIssuer.Issuer = ""
	if _, err := newOIDC(context.Background(), noIssuer); err == nil {
		t.Fatal("want an error for verificationOnly without an issuer")
	}
}

// Every entry point into the authorization-code flow refuses out loud, rather
// than handing back an authorization URL with no redirect_uri.
func TestVerificationOnlyRefusesTheLoginFlow(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p, err := newOIDC(context.Background(), oidcConfig{
		Type: "oidc", Issuer: f.srv.URL, ClientID: "client-1", VerificationOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("AuthCodeURL panics", func(t *testing.T) {
		defer func() {
			v := recover()
			if v == nil {
				t.Fatal("AuthCodeURL must panic on a verificationOnly connector, not return a URL")
			}
			msg, _ := v.(string)
			if !strings.Contains(msg, "verificationOnly") || !strings.Contains(msg, "login flow") {
				t.Fatalf("panic message = %v; want it to name verificationOnly and the login flow", v)
			}
		}()
		_ = p.AuthCodeURL("state", "nonce", "verifier")
	})

	if _, _, err := p.Exchange(context.Background(), "code", "verifier", "nonce"); err == nil ||
		!strings.Contains(err.Error(), "verificationOnly") {
		t.Fatalf("Exchange error = %v; want a refusal naming verificationOnly", err)
	}
	if _, err := p.Refresh(context.Background(), "refresh-token"); err == nil ||
		!strings.Contains(err.Error(), "verificationOnly") {
		t.Fatalf("Refresh error = %v; want a refusal naming verificationOnly", err)
	}
	// There is no provider session to end, so RP-initiated logout is unsupported —
	// even though the fake IdP advertises an end_session_endpoint.
	if u, ok := p.LogoutURL("hint", "https://app/"); ok || u != "" {
		t.Fatalf("LogoutURL = %q, %v; want unsupported", u, ok)
	}
}

// A full connector keeps the behaviour it has today: IsVerificationOnly is
// false and the login flow works.
func TestFullConnectorIsNotVerificationOnly(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	p := buildOIDC(t, f)
	if IsVerificationOnly(p) {
		t.Fatal("a connector that does not declare verificationOnly must not report as one")
	}
	if !strings.Contains(p.AuthCodeURL("s", "n", "v"), "redirect_uri=") {
		t.Fatal("a full connector must still produce an authorization URL with a redirect_uri")
	}
}

func TestIsVerificationOnlyNil(t *testing.T) {
	if IsVerificationOnly(nil) {
		t.Fatal("a nil IdP is not verification-only")
	}
}
