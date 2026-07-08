package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSealer(t *testing.T) *sealer {
	t.Helper()
	s, err := newSealer32([]byte(key32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSealOpenRoundTripAndTamper(t *testing.T) {
	s := newSealer(t)
	ct, err := s.seal([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s.open(ct)
	if err != nil || string(pt) != "hello world" {
		t.Fatalf("round-trip: %q %v", pt, err)
	}
	bad := []byte(ct)
	bad[len(bad)/2] ^= 0x01
	if _, err := s.open(string(bad)); err == nil {
		t.Fatal("tampered ciphertext must fail to open")
	}
	if _, err := s.open("!!!not base64!!!"); err == nil {
		t.Fatal("garbage must fail to open")
	}
}

func TestWriteReadChunksRoundTrip(t *testing.T) {
	s := newSealer(t)
	big := []byte(strings.Repeat("A", 9000))
	w := httptest.NewRecorder()
	writeChunkedCookie(w, "hog_session", mustSeal(t, s, big), true)
	cookies := w.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatalf("expected ≥2 chunk cookies, got %d", len(cookies))
	}
	for _, c := range cookies {
		if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || !c.Secure {
			t.Fatalf("attrs wrong: %+v", c)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	joined, ok := readChunkedCookie(r, "hog_session")
	if !ok {
		t.Fatal("read failed")
	}
	got, err := s.open(joined)
	if err != nil || len(got) != 9000 {
		t.Fatalf("reassembled open: len=%d err=%v", len(got), err)
	}
}

func TestReadIgnoresNonContiguousAndJunkChunks(t *testing.T) {
	// A valid contiguous set survives injected out-of-range/negative/junk chunks.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "hog_session.0", Value: "a"})
	r.AddCookie(&http.Cookie{Name: "hog_session.1", Value: "b"})
	r.AddCookie(&http.Cookie{Name: "hog_session.999", Value: "junk"})
	r.AddCookie(&http.Cookie{Name: "hog_session.-5", Value: "neg"})
	r.AddCookie(&http.Cookie{Name: "hog_session.x", Value: "nonnum"})
	if got, ok := readChunkedCookie(r, "hog_session"); !ok || got != "ab" {
		t.Fatalf("contiguous read = %q %v, want \"ab\" true (junk ignored)", got, ok)
	}
	// Missing .0 ⇒ ok=false.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(&http.Cookie{Name: "hog_session.1", Value: "b"})
	if _, ok := readChunkedCookie(r2, "hog_session"); ok {
		t.Fatal("missing .0 must yield ok=false")
	}
	// Gap after .0 (.0,.2) ⇒ only the contiguous prefix is returned; the truncated
	// blob fails GCM open downstream.
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.AddCookie(&http.Cookie{Name: "hog_session.0", Value: "a"})
	r3.AddCookie(&http.Cookie{Name: "hog_session.2", Value: "c"})
	if got, ok := readChunkedCookie(r3, "hog_session"); !ok || got != "a" {
		t.Fatalf("gap read = %q %v, want \"a\" true (contiguous prefix only)", got, ok)
	}
}

func TestClearExpiresAllChunks(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "hog_session.0", Value: "a"})
	r.AddCookie(&http.Cookie{Name: "hog_session.1", Value: "b"})
	w := httptest.NewRecorder()
	clearChunkedCookie(w, r, "hog_session", true)
	cleared := w.Result().Cookies()
	if len(cleared) != 2 {
		t.Fatalf("expected 2 cleared cookies, got %d", len(cleared))
	}
	for _, c := range cleared {
		if c.MaxAge >= 0 || !c.Secure {
			t.Fatalf("cookie %q not expired/secure: %+v", c.Name, c)
		}
	}
}

func TestWriteChunkBoundary(t *testing.T) {
	w1 := httptest.NewRecorder()
	writeChunkedCookie(w1, "s", strings.Repeat("x", maxChunk), true)
	if n := len(w1.Result().Cookies()); n != 1 {
		t.Fatalf("exactly maxChunk ⇒ 1 cookie, got %d", n)
	}
	w2 := httptest.NewRecorder()
	writeChunkedCookie(w2, "s", strings.Repeat("x", maxChunk+1), true)
	if n := len(w2.Result().Cookies()); n != 2 {
		t.Fatalf("maxChunk+1 ⇒ 2 cookies, got %d", n)
	}
}

func mustSeal(t *testing.T, s *sealer, b []byte) string {
	t.Helper()
	ct, err := s.seal(b)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}
