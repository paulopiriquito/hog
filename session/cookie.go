package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// maxChunk is the per-cookie value budget. Browsers cap a cookie around 4 KB
// (name+value); 3900 leaves room for the name and attributes.
const maxChunk = 3900

// sealer is an AES-256-GCM authenticated-encryption helper.
type sealer struct{ aead cipher.AEAD }

func newSealer32(key []byte) (*sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("session: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

// seal encrypts plaintext → base64url(nonce ‖ ciphertext ‖ tag).
func (s *sealer) seal(plaintext []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// AAD intentionally nil: the whole sealed blob is one ciphertext (chunking is a
	// transport detail); identity (subject, fingerprint) is bound inside the plaintext.
	sealed := s.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// open reverses seal; returns an error on any decode/auth failure.
func (s *sealer) open(value string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return nil, fmt.Errorf("session: ciphertext too short")
	}
	return s.aead.Open(nil, raw[:ns], raw[ns:], nil)
}

// writeChunkedCookie splits value across <name>.0..N cookies and sets them.
func writeChunkedCookie(w http.ResponseWriter, name, value string, secure bool) {
	for i := 0; len(value) > 0; i++ {
		n := maxChunk
		if len(value) < n {
			n = len(value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     fmt.Sprintf("%s.%d", name, i),
			Value:    value[:n],
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
		value = value[n:]
	}
}

// readChunkedCookie reassembles the maximal contiguous run of <name>.0,.1,…
// cookies. Non-numeric, negative, and non-contiguous (e.g. an injected high
// index) chunks are ignored, so a cookie an attacker tosses onto the domain
// cannot deny a legitimate session read. ok=false only when <name>.0 is absent.
// Integrity is enforced downstream by GCM open, not here.
func readChunkedCookie(r *http.Request, name string) (string, bool) {
	parts := map[int]string{}
	prefix := name + "."
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, prefix) {
			if idx, err := strconv.Atoi(c.Name[len(prefix):]); err == nil && idx >= 0 {
				parts[idx] = c.Value
			}
		}
	}
	var b strings.Builder
	n := 0
	for {
		v, ok := parts[n]
		if !ok {
			break
		}
		b.WriteString(v)
		n++
	}
	if n == 0 {
		return "", false
	}
	return b.String(), true
}

// clearChunkedCookie expires every <name>.N chunk present on the request.
func clearChunkedCookie(w http.ResponseWriter, r *http.Request, name string, secure bool) {
	prefix := name + "."
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, prefix) {
			http.SetCookie(w, &http.Cookie{
				Name: c.Name, Value: "", Path: "/", HttpOnly: true,
				Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
			})
		}
	}
}
