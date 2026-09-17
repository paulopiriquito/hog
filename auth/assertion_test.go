package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/v2/session"
)

func genAssertionKeys(t *testing.T) (seed []byte, pub ed25519.PublicKey) {
	t.Helper()
	p, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv.Seed(), p
}

func TestAssertionRoundTrip(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := &session.Principal{
		Subject:     "u-1",
		Passport:    map[string]any{"email": "u1@x.co"},
		Groups:      []string{"dev", "admin"},
		AccessToken: "should-never-appear",
	}
	token, err := iss.Mint(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, "should-never-appear") {
		t.Fatal("assertion must never carry the principal's access token")
	}

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	got, err := v.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Subject != "u-1" {
		t.Fatalf("subject = %q, want u-1", got.Subject)
	}
	if got.Passport["email"] != "u1@x.co" {
		t.Fatalf("passport = %v", got.Passport)
	}
	if len(got.Groups) != 2 || got.Groups[0] != "dev" || got.Groups[1] != "admin" {
		t.Fatalf("groups = %v", got.Groups)
	}
}

func TestAssertionHasThreeParts(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3 (%q)", len(parts), token)
	}
}

func TestAssertionRejectsWrongIssuer(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("some-other-app", map[string]ed25519.PublicKey{"k1": pub})
	if _, err := v.Verify(token); err == nil {
		t.Fatal("want error for a mismatched issuer")
	}
}

func TestAssertionRejectsUnknownKid(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, otherPub := genAssertionKeys(t)
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k2": otherPub})
	if _, err := v.Verify(token); err == nil {
		t.Fatal("want error for an unknown kid (must not fall back to the only key)")
	}
}

func TestAssertionRejectsWrongKey(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPub := genAssertionKeys(t)
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": wrongPub})
	if _, err := v.Verify(token); err == nil {
		t.Fatal("want error when the kid resolves to the wrong key")
	}
}

func TestAssertionRejectsTamperedPayload(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatal(err)
	}
	claims["sub"] = "u-2" // escalate to a different subject
	tamperedJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(tamperedJSON) + "." + parts[2]

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	if _, err := v.Verify(tampered); err == nil {
		t.Fatal("want error for a tampered payload")
	}
}

func TestAssertionRejectsGarbage(t *testing.T) {
	_, pub := genAssertionKeys(t)
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	if _, err := v.Verify("not-a-real-token"); err == nil {
		t.Fatal("want error for garbage input")
	}
}

// TestAssertionAlgCheckIsDefenseInDepth crafts a token whose header claims a
// different alg (by re-encoding the header only) and keeps the original
// signature. There is no weaker-algorithm dispatch in this design — the
// signature check would already fail on its own here — so this is a
// defence-in-depth assertion, not a demonstration of a closed exploit; it
// would still pass even if the alg gate itself were deleted, since the
// signature (computed over the original, EdDSA header) no longer matches the
// forged one. TestAssertionAlgGateRunsBeforeKidLookup below asserts the gate
// itself, directly.
func TestAssertionAlgCheckIsDefenseInDepth(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	badHeader, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": "k1"})
	if err != nil {
		t.Fatal(err)
	}
	forged := base64.RawURLEncoding.EncodeToString(badHeader) + "." + parts[1] + "." + parts[2]

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	if _, err := v.Verify(forged); err == nil {
		t.Fatal("want error for a non-EdDSA alg (alg confusion)")
	}
}

// TestAssertionAlgGateRunsBeforeKidLookup proves the alg check runs before any
// kid lookup: a forged header naming BOTH an unknown kid and a non-EdDSA alg
// is rejected with the alg error, not the kid one, so an attacker cannot use
// an unrecognized kid to probe whether alg is even checked.
func TestAssertionAlgGateRunsBeforeKidLookup(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	badHeader, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": "does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	forged := base64.RawURLEncoding.EncodeToString(badHeader) + "." + parts[1] + "." + parts[2]

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	_, err = v.Verify(forged)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "unsupported alg") {
		t.Fatalf("err = %v, want the alg error (alg must be checked before the kid lookup)", err)
	}
	if strings.Contains(err.Error(), "unknown kid") {
		t.Fatalf("err = %v, must not be the kid error", err)
	}
}

func TestAssertionRejectsExpired(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	iss.now = func() time.Time { return base }
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	v.now = func() time.Time { return base.Add(time.Minute + assertionSkew + time.Second) }
	_, err = v.Verify(token)
	if !errors.Is(err, ErrAssertionExpired) {
		t.Fatalf("err = %v, want ErrAssertionExpired", err)
	}
}

func TestAssertionRejectsIssuedFarInFuture(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	iss.now = func() time.Time { return base.Add(time.Hour) }
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}

	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	v.now = func() time.Time { return base }
	_, err = v.Verify(token)
	if err == nil {
		t.Fatal("want error for an assertion issued far in the future")
	}
	if errors.Is(err, ErrAssertionExpired) {
		t.Fatal("issued-in-the-future must be distinct from expiry")
	}
}

func TestAssertionRejectsEmptySubject(t *testing.T) {
	seed, pub := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: ""})
	if err != nil {
		t.Fatal(err)
	}
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{"k1": pub})
	if _, err := v.Verify(token); err == nil {
		t.Fatal("want error for an empty subject")
	}
}

func TestNewAssertionIssuerRejectsBadSeed(t *testing.T) {
	if _, err := NewAssertionIssuer("go-app", "k1", make([]byte, 16), time.Minute); err == nil {
		t.Fatal("want error for a seed that is not 32 bytes")
	}
}

// TestNewAssertionVerifierDropsWrongLengthKeys proves a wrong-length public
// key never makes it into the verifier's key map: ed25519.Verify panics on
// one, unlike a bad signature (which just returns false), so it must never be
// reachable from configured keys.
func TestNewAssertionVerifierDropsWrongLengthKeys(t *testing.T) {
	_, pub := genAssertionKeys(t)
	v := NewAssertionVerifier("go-app", map[string]ed25519.PublicKey{
		"k1":  pub,
		"bad": make(ed25519.PublicKey, 4), // wrong length; must be dropped, not admitted
	})
	if _, ok := v.keys["bad"]; ok {
		t.Fatal("wrong-length key must be dropped by NewAssertionVerifier")
	}
	if _, ok := v.keys["k1"]; !ok {
		t.Fatal("valid key must be kept")
	}
}

// TestVerifyRejectsWrongLengthKeyWithoutPanicking guards Verify's own length
// check directly, bypassing NewAssertionVerifier (as a caller building the key
// map some other way would): a wrong-length key under a matching kid must be
// rejected with an error, never panic ed25519.Verify.
func TestVerifyRejectsWrongLengthKeyWithoutPanicking(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := iss.Mint(&session.Principal{Subject: "u-1"})
	if err != nil {
		t.Fatal(err)
	}

	v := &AssertionVerifier{
		issuer: "go-app",
		keys:   map[string]ed25519.PublicKey{"k1": make(ed25519.PublicKey, 4)},
		now:    time.Now,
	}
	if _, err := v.Verify(token); err == nil {
		t.Fatal("want an error for a wrong-length key, not a panic")
	}
}

// TestAssertionIssuerStringRedactsKey proves String() does not fall back to
// the default %v/%+v reflection dump, which would print the raw signing key.
func TestAssertionIssuerStringRedactsKey(t *testing.T) {
	seed, _ := genAssertionKeys(t)
	iss, err := NewAssertionIssuer("go-app", "k1", seed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%+v", iss)
	rawKeyDump := fmt.Sprintf("%v", iss.key) // what a naive %+v would have printed for the unexported field
	if strings.Contains(got, rawKeyDump) {
		t.Fatalf("formatted issuer leaks the raw key: %q", got)
	}
	if !strings.Contains(got, "go-app") || !strings.Contains(got, "k1") {
		t.Fatalf("formatted issuer should name the issuer and kid, got %q", got)
	}
}
