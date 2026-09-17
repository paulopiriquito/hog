package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/v2/session"
)

// DefaultAssertionHeader carries the identity assertion between HOG instances.
const DefaultAssertionHeader = "X-Hog-Identity"

// assertionSkew tolerates clock drift between the two instances.
const assertionSkew = 30 * time.Second

var (
	ErrAssertionInvalid = errors.New("identity assertion: invalid")
	ErrAssertionExpired = errors.New("identity assertion: expired")
)

// IdentityAssertion is the signed statement one HOG instance makes about the
// principal it resolved, for another instance to accept without calling the IdP.
type IdentityAssertion struct {
	Issuer    string         `json:"iss"`
	Subject   string         `json:"sub"`
	IssuedAt  int64          `json:"iat"`
	ExpiresAt int64          `json:"exp"`
	Passport  map[string]any `json:"passport,omitempty"`
	Groups    []string       `json:"groups,omitempty"`
}

type assertionHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// AssertionIssuer mints assertions with one Ed25519 key.
type AssertionIssuer struct {
	name, kid string
	key       ed25519.PrivateKey
	ttl       time.Duration
	now       func() time.Time
}

// NewAssertionIssuer derives the signing key from a 32-byte seed.
func NewAssertionIssuer(name, kid string, seed []byte, ttl time.Duration) (*AssertionIssuer, error) {
	if name == "" {
		return nil, fmt.Errorf("identity assertion: issuer name is required")
	}
	if kid == "" {
		return nil, fmt.Errorf("identity assertion: kid is required")
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity assertion: seed must be %d bytes (got %d)", ed25519.SeedSize, len(seed))
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &AssertionIssuer{
		name: name,
		kid:  kid,
		key:  ed25519.NewKeyFromSeed(seed),
		ttl:  ttl,
		now:  time.Now,
	}, nil
}

// PublicKey is the verification key to hand to the accepting instance.
func (i *AssertionIssuer) PublicKey() ed25519.PublicKey {
	return i.key.Public().(ed25519.PublicKey)
}

// String redacts the signing key: fmt prints unexported fields for %v/%+v, and
// AssertionIssuer holds the raw ed25519.PrivateKey in one, so without this
// override a stray log line formatting the issuer would leak it.
func (i *AssertionIssuer) String() string {
	return fmt.Sprintf("AssertionIssuer{name: %s, kid: %s}", i.name, i.kid)
}

// Mint signs the principal's identity (subject, passport, groups) — never its access token.
func (i *AssertionIssuer) Mint(p *session.Principal) (string, error) {
	now := i.now()
	// Copy rather than alias: p is the caller-owned, live request-context
	// principal and outlives this call, so claims must not keep a handle into
	// its mutable map/slice.
	var passport map[string]any
	if p.Passport != nil {
		passport = make(map[string]any, len(p.Passport))
		for k, v := range p.Passport {
			passport[k] = v
		}
	}
	claims := IdentityAssertion{
		Issuer:    i.name,
		Subject:   p.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(i.ttl).Unix(),
		Passport:  passport,
		Groups:    append([]string(nil), p.Groups...),
	}
	headJSON, err := json.Marshal(assertionHeader{Alg: "EdDSA", Typ: "JWT", Kid: i.kid})
	if err != nil {
		return "", fmt.Errorf("identity assertion: encode header: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("identity assertion: encode payload: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := ed25519.Sign(i.key, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// AssertionVerifier checks assertions from one issuer against its known keys.
type AssertionVerifier struct {
	issuer string
	keys   map[string]ed25519.PublicKey
	now    func() time.Time
}

// NewAssertionVerifier verifies assertions claiming iss==issuer, signed by one
// of keys (kid -> public key). An entry whose key is not exactly
// ed25519.PublicKeySize bytes is dropped rather than admitted: ed25519.Verify
// panics on a wrong-length key (unlike a bad signature, which just returns
// false), so keeping it would let a request turn into a process panic.
func NewAssertionVerifier(issuer string, keys map[string]ed25519.PublicKey) *AssertionVerifier {
	safe := make(map[string]ed25519.PublicKey, len(keys))
	for kid, key := range keys {
		if len(key) == ed25519.PublicKeySize {
			safe[kid] = key
		}
	}
	return &AssertionVerifier{issuer: issuer, keys: safe, now: time.Now}
}

// Verify checks the compact JWS: three parts, an EdDSA header naming a known kid,
// the signature, the issuer, expiry (with skew) and a not-in-the-future iat.
func (v *AssertionVerifier) Verify(token string) (*IdentityAssertion, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not a compact JWS", ErrAssertionInvalid)
	}
	headJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header: %w", ErrAssertionInvalid, err)
	}
	var head assertionHeader
	if err := json.Unmarshal(headJSON, &head); err != nil {
		return nil, fmt.Errorf("%w: header: %w", ErrAssertionInvalid, err)
	}
	// Reject before any key lookup or signature work: an attacker who can pick
	// alg could otherwise redirect verification to a weaker (or absent) scheme.
	if head.Alg != "EdDSA" {
		return nil, fmt.Errorf("%w: unsupported alg %q", ErrAssertionInvalid, head.Alg)
	}
	key, ok := v.keys[head.Kid]
	if !ok || len(key) != ed25519.PublicKeySize {
		// A configured verifier never holds a wrong-length key (see
		// NewAssertionVerifier), but ed25519.Verify PANICS below on one — unlike
		// a bad signature, which just returns false — so any other caller
		// building the key map directly must not reach it.
		return nil, fmt.Errorf("%w: unknown kid %q", ErrAssertionInvalid, head.Kid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %w", ErrAssertionInvalid, err)
	}
	// Verify over the exact original first two segments — never re-encode them,
	// or a semantically-equivalent-but-differently-serialized JSON payload
	// could pass verification.
	signingInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(key, []byte(signingInput), sig) {
		return nil, fmt.Errorf("%w: signature mismatch", ErrAssertionInvalid)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %w", ErrAssertionInvalid, err)
	}
	var claims IdentityAssertion
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload: %w", ErrAssertionInvalid, err)
	}
	if claims.Issuer != v.issuer {
		return nil, fmt.Errorf("%w: unexpected issuer %q", ErrAssertionInvalid, claims.Issuer)
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: empty subject", ErrAssertionInvalid)
	}
	now := v.now()
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(assertionSkew)) {
		return nil, ErrAssertionExpired
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(assertionSkew)) {
		return nil, fmt.Errorf("%w: issued in the future", ErrAssertionInvalid)
	}
	return &claims, nil
}
