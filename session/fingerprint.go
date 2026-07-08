package session

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// computeFingerprint derives a stable, server-side fingerprint from the
// configured request headers: base64(sha256("h1=v1\nh2=v2\n…")).
func computeFingerprint(headers []string, r *http.Request) string {
	var b strings.Builder
	for _, h := range headers {
		b.WriteString(h)
		b.WriteByte('=')
		b.WriteString(r.Header.Get(h))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// fingerprintEqual compares two fingerprints in constant time.
func fingerprintEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
