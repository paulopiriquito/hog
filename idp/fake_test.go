package idp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeIdP is a minimal in-process OIDC provider for tests: it serves discovery,
// a JWKS, and a token endpoint that signs+returns an id_token built from
// idTokenClaims (so tests control sub/email/name/nonce/aud/exp). accessPriv is
// a second key, used to mirror providers that sign access tokens with a key
// set distinct from the ID-token jwks_uri.
type fakeIdP struct {
	srv           *httptest.Server
	priv          *rsa.PrivateKey
	accessPriv    *rsa.PrivateKey
	clientID      string
	idTokenClaims map[string]any
	signingAlgs   []string // id_token_signing_alg_values_supported; default ["RS256"], settable before newOIDC
}

// newFakeIdP spins up a fake IdP that advertises a dedicated access-token JWKS
// via jwks_access_token_uri, as some identity providers do.
func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	return newFakeIdPWith(t, clientID, true)
}

// newFakeIdPNoAccessJWKS spins up a fake IdP that does not advertise
// jwks_access_token_uri, so access tokens are expected to verify against the
// ID-token jwks_uri like an id_token would — the shape legacy tests rely on.
func newFakeIdPNoAccessJWKS(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	return newFakeIdPWith(t, clientID, false)
}

func newFakeIdPWith(t *testing.T, clientID string, advertiseAccessJWKS bool) *fakeIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	accessPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{priv: priv, accessPriv: accessPriv, clientID: clientID, idTokenClaims: map[string]any{}, signingAlgs: []string{"RS256"}}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"},
	}}
	accessJWKS := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &accessPriv.PublicKey, KeyID: "access-key", Algorithm: "RS256", Use: "sig"},
	}}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		disco := map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/auth",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"end_session_endpoint":                  f.srv.URL + "/logout",
			"id_token_signing_alg_values_supported": f.signingAlgs,
			"userinfo_endpoint":                     f.srv.URL + "/userinfo",
		}
		if advertiseAccessJWKS {
			disco["jwks_access_token_uri"] = f.srv.URL + "/ext/oauth/jwks"
		}
		writeJSON(w, disco)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, jwks) })
	mux.HandleFunc("/ext/oauth/jwks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, accessJWKS) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"access_token":  "fake-access-token",
			"id_token":      f.sign(t, f.idTokenClaims),
			"refresh_token": "fake-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"sub": "user-123", "email": "ui@example.com", "isMemberOf": []string{"cn=admins,ou=applicationRole"}})
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signWith(t, f.priv, f.srv.URL, f.clientID, claims)
}

// signRaw signs exactly the given claims with priv (no default sub/aud/iss/exp):
// the shape of a subjectless access token is under the test's control.
func signRaw(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: priv, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// subjectlessAccessToken mirrors a provider that issues iss, exp, iat,
// client_id and uid — no sub, no aud.
func (f *fakeIdP) subjectlessAccessToken(t *testing.T, clientID, uid string) string {
	t.Helper()
	now := time.Now()
	return signRaw(t, f.accessPriv, "access-key", map[string]any{
		"iss":       f.srv.URL,
		"client_id": clientID,
		"uid":       uid,
		"scope":     []string{"openid"},
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
	})
}

func signWith(t *testing.T, priv *rsa.PrivateKey, iss, aud string, claims map[string]any) string {
	t.Helper()
	c := map[string]any{
		"iss": iss,
		"aud": aud,
		"sub": "user-123",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range claims {
		c[k] = v
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: priv, KeyID: "test-key"}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
