// Package auth implements HOG's browser BFF auth: the OIDC login/callback/logout
// endpoints (this sub-project, #3a) and, later, the session-resolve / auth-gate /
// projection chain gates (#3b).
package auth

import (
	"encoding/json"
	"strings"

	"github.com/paulopiriquito/hog/session"
)

// loginAAD domain-separates the transient login cookie from the session cookie
// (both sealed with the same key).
var loginAAD = []byte("hog/login/v1")

// loginState is the transient state carried across login → IdP → callback in the
// short-lived encrypted hog_login cookie. Verifier is empty when PKCE is disabled.
type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	ReturnTo string `json:"r"`
}

func sealLoginState(sealer *session.Sealer, ls loginState) (string, error) {
	b, err := json.Marshal(ls)
	if err != nil {
		return "", err
	}
	return sealer.Seal(b, loginAAD)
}

func openLoginState(sealer *session.Sealer, value string) (loginState, error) {
	raw, err := sealer.Open(value, loginAAD)
	if err != nil {
		return loginState{}, err
	}
	var ls loginState
	if err := json.Unmarshal(raw, &ls); err != nil {
		return loginState{}, err
	}
	return ls, nil
}

// safeReturnTo accepts only a local path (single leading slash, no scheme/host,
// not protocol-relative). Anything else falls back to "/".
func safeReturnTo(raw string) string {
	if raw == "" || raw[0] != '/' {
		return "/"
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return "/" // protocol-relative
	}
	if strings.Contains(raw, "://") {
		return "/"
	}
	return raw
}
