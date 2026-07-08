package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/session"
	"golang.org/x/oauth2"
)

const loginCookie = "hog_login"
const loginTTL = 600 // seconds

// Handlers serves the browser auth endpoints.
type Handlers struct {
	idp    idp.IdP
	sess   session.Manager
	sealer *session.Sealer
	cfg    Config
	scfg   session.Config
	log    *slog.Logger
}

// NewHandlers builds the auth endpoint handlers.
func NewHandlers(i idp.IdP, s session.Manager, sealer *session.Sealer, cfg Config, scfg session.Config) *Handlers {
	return &Handlers{idp: i, sess: s, sealer: sealer, cfg: cfg, scfg: scfg, log: slog.Default()}
}

func randToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func secure(r *http.Request) bool {
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return p == "https"
	}
	return true
}

// Login starts the OIDC flow: seal transient state, redirect to the IdP.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	ls := loginState{
		State:    randToken(),
		Nonce:    randToken(),
		ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")),
	}
	if h.idp.UsesPKCE() {
		ls.Verifier = oauth2.GenerateVerifier()
	}
	sealed, err := sealLoginState(h.sealer, ls)
	if err != nil {
		h.log.Error("auth: seal login state", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: loginCookie, Value: sealed, Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: loginTTL,
	})
	http.Redirect(w, r, h.idp.AuthCodeURL(ls.State, ls.Nonce, ls.Verifier), http.StatusFound)
}

// Callback completes the OIDC flow: verify state, exchange, build the session.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	raw, err := r.Cookie(loginCookie)
	if err != nil {
		http.Error(w, "login session expired; please restart sign-in", http.StatusBadRequest)
		return
	}
	ls, err := openLoginState(h.sealer, raw.Value)
	if err != nil {
		http.Error(w, "login session invalid; please restart sign-in", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		http.Error(w, "identity provider returned an error", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(ls.State)) != 1 {
		http.Error(w, "invalid authentication response", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tokens, identity, err := h.idp.Exchange(ctx, code, ls.Verifier, ls.Nonce)
	if err != nil {
		h.log.Error("auth: token exchange", "err", err)
		http.Error(w, "authentication failed", http.StatusBadGateway)
		return
	}
	var userinfo map[string]any
	if NeedsUserInfo(h.scfg, h.cfg.UserInfo) {
		userinfo, err = h.idp.UserInfo(ctx, tokens.AccessToken)
		if err != nil {
			h.log.Error("auth: userinfo", "err", err)
			http.Error(w, "authentication failed", http.StatusBadGateway)
			return
		}
	}
	s := h.sess.New(identity, userinfo, tokens, r)
	if err := h.sess.Write(w, r, s); err != nil {
		h.log.Error("auth: write session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Single-use: expire the login cookie.
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, ls.ReturnTo, http.StatusFound)
}

// Logout clears the HOG session and redirects to the post-logout page. HOG-only
// logout: the IdP session is not touched.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	h.sess.Clear(w, r)
	http.Redirect(w, r, h.scfg.PostLogoutRedirect, http.StatusFound)
}
