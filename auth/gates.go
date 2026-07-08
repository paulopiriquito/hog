package auth

import (
	"net/http"
	"net/url"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/session"
)

// SessionGate resolves the session cookie into a request-context Principal.
// A missing/expired/invalid session proceeds unauthenticated (no silent refresh
// in the stateless mode). A nil manager passes through unchanged.
func SessionGate(m session.Manager) chain.Middleware {
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if m != nil {
				if s, err := m.Read(r); err == nil {
					r = r.WithContext(session.WithPrincipal(r.Context(), s.Principal()))
				}
			}
			next.ServeHTTP(w, r)
		})
	})
}

// loginRedirectURL builds "<loginPath>?...&return_to=<uri>", preserving any
// query already present on loginPath and percent-encoding return_to correctly.
func loginRedirectURL(loginPath, returnTo string) string {
	u, err := url.Parse(loginPath)
	if err != nil {
		// loginPath is operator config; on the unlikely parse failure fall back
		// to a safe concatenation rather than dropping the redirect.
		return loginPath + "?return_to=" + url.QueryEscape(returnTo)
	}
	q := u.Query()
	q.Set("return_to", returnTo)
	u.RawQuery = q.Encode()
	return u.String()
}

// AuthGate enforces a route's auth requirement. When required and there is no
// principal in context: an App route redirects (302) to loginPath with a
// return_to of the current request URI; a Service route returns 401. When not
// required, or a principal is present, it passes through.
func AuthGate(required, isApp bool, loginPath string) chain.Middleware {
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if required {
				if _, ok := session.FromContext(r.Context()); !ok {
					if isApp {
						http.Redirect(w, r, loginRedirectURL(loginPath, r.URL.RequestURI()), http.StatusFound)
					} else {
						http.Error(w, "unauthorized", http.StatusUnauthorized)
					}
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
}
