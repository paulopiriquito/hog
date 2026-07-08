package auth

import (
	"testing"

	"github.com/paulopiriquito/hog/session"
)

const key32 = "0123456789abcdef0123456789abcdef"

func TestLoginStateRoundTripAndTamper(t *testing.T) {
	sealer, err := session.NewSealer([]byte(key32))
	if err != nil {
		t.Fatal(err)
	}
	ls := loginState{State: "st", Nonce: "no", Verifier: "ve", ReturnTo: "/app"}
	sealed, err := sealLoginState(sealer, ls)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openLoginState(sealer, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != ls {
		t.Fatalf("round-trip = %+v want %+v", got, ls)
	}
	if _, err := openLoginState(sealer, "@@@garbage@@@"); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestLoginCookieNotOpenableAsSession(t *testing.T) {
	sealer, _ := session.NewSealer([]byte(key32))
	sealed, _ := sealLoginState(sealer, loginState{State: "x"})
	// Opening the login blob with the SESSION domain label must fail (AAD mismatch).
	if _, err := sealer.Open(sealed, []byte("hog/session/v1")); err == nil {
		t.Fatal("login cookie must not open under the session AAD")
	}
}

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"/app":           "/app",
		"/app/x?y=1":     "/app/x?y=1",
		"":               "/",
		"//evil.com":     "/",
		"/\\evil.com":    "/",
		"https://evil":   "/",
		"http://evil":    "/",
		"javascript:foo": "/",
		"app/no-slash":   "/",
		"/":              "/",
	}
	for in, want := range cases {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}
