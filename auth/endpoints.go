package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/v2/idp"
	"github.com/paulopiriquito/hog/v2/session"
	"golang.org/x/oauth2"
)

// loginCookie prefixes the transient login cookies. Each sign-in gets its own,
// named after its state, so two sign-ins in flight at once (two tabs, or a tab
// and a request the person never sees) cannot overwrite each other.
const loginCookie = "hog_login"
const loginTTL = 600 // seconds

// maxLoginFlows bounds the login cookies a browser carries at once. Every
// abandoned sign-in leaves one behind for loginTTL; at this many, a new sign-in
// clears the others so the Cookie header stays small.
const maxLoginFlows = 5

// loginCookieName names the login cookie of the sign-in that issued state.
func loginCookieName(state string) string {
	sum := sha256.Sum256([]byte(state))
	return loginCookie + "_" + hex.EncodeToString(sum[:8])
}

// loginCookies returns the login cookies on r, including a plain hog_login one
// written before sign-ins had their own.
func loginCookies(r *http.Request) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == loginCookie || strings.HasPrefix(c.Name, loginCookie+"_") {
			out = append(out, c)
		}
	}
	return out
}

func expireLoginCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// Handlers serves the browser auth endpoints.
type Handlers struct {
	idp    idp.IdP
	sess   session.Manager
	sealer *session.Sealer
	cfg    Config
	scfg   session.Config
	idCfg  session.IdentityConfig
	log    *slog.Logger
}

// NewHandlers builds the auth endpoint handlers.
func NewHandlers(i idp.IdP, s session.Manager, sealer *session.Sealer, cfg Config, scfg session.Config, idCfg session.IdentityConfig) *Handlers {
	return &Handlers{idp: i, sess: s, sealer: sealer, cfg: cfg, scfg: scfg, idCfg: idCfg, log: slog.Default()}
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
	if flows := loginCookies(r); len(flows) >= maxLoginFlows {
		for _, c := range flows {
			expireLoginCookie(w, r, c.Name)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: loginCookieName(ls.State), Value: sealed, Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: loginTTL,
	})
	http.Redirect(w, r, h.idp.AuthCodeURL(ls.State, ls.Nonce, ls.Verifier), http.StatusFound)
}

// Callback completes the OIDC flow: verify state, exchange, build the session.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	// A cookie written before sign-ins had their own is still read, so a
	// sign-in in flight across an upgrade completes.
	raw, err := r.Cookie(loginCookieName(state))
	if err != nil {
		raw, err = r.Cookie(loginCookie)
	}
	if err != nil {
		h.rejectCallback(w, r, "login session expired; please restart sign-in")
		return
	}
	ls, err := openLoginState(h.sealer, raw.Value)
	if err != nil {
		h.rejectCallback(w, r, "login session invalid; please restart sign-in")
		return
	}
	if q.Get("error") != "" {
		http.Error(w, "identity provider returned an error", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(ls.State)) != 1 {
		h.rejectCallback(w, r, "invalid authentication response")
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
	if NeedsUserInfo(h.idCfg) {
		userinfo, err = h.idp.UserInfo(ctx, tokens.AccessToken)
		if err != nil {
			h.log.Error("auth: userinfo", "err", err)
			http.Error(w, "authentication failed", http.StatusBadGateway)
			return
		}
	}
	if err := h.sess.Issue(w, r, identity, userinfo, tokens); err != nil {
		h.log.Error("auth: write session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Single-use: expire this sign-in's login cookie.
	expireLoginCookie(w, r, raw.Name)
	http.Redirect(w, r, ls.ReturnTo, http.StatusFound)
}

// rejectCallback answers a callback that cannot be matched to its sign-in. When
// the browser already holds a valid session another sign-in finished first, so
// the person goes on to the app instead of to an error page. Nothing from the
// callback is used on that path: no code is exchanged and no session is issued.
func (h *Handlers) rejectCallback(w http.ResponseWriter, r *http.Request, msg string) {
	if _, err := h.sess.Read(r); err == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	http.Error(w, msg, http.StatusBadRequest)
}

// Logout clears the HOG session and redirects to the post-logout page. HOG-only
// logout: the IdP session is not touched.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	h.sess.Clear(w, r)
	http.Redirect(w, r, h.scfg.PostLogoutRedirect, http.StatusFound)
}
