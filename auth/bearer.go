package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/session"
)

// BearerVerifier validates a Bearer access token and fetches userinfo. idp.IdP
// satisfies it; tests use a narrow fake. VerifyAccessToken must return a non-nil
// error on failure (a nil Identity is treated as an invalid token, fail-closed).
type BearerVerifier interface {
	VerifyAccessToken(ctx context.Context, rawJWT string) (*idp.Identity, error)
	UserInfo(ctx context.Context, accessToken string) (map[string]any, error)
}

type bearerErrKey struct{}

// withBearerError marks the context: a Bearer token was present but failed
// verification (so AuthGate can answer with error="invalid_token").
func withBearerError(ctx context.Context) context.Context {
	return context.WithValue(ctx, bearerErrKey{}, true)
}

func hasBearerError(ctx context.Context) bool {
	v, _ := ctx.Value(bearerErrKey{}).(bool)
	return v
}

// bearerChallenge is the WWW-Authenticate value for a Service 401.
func bearerChallenge(ctx context.Context) string {
	if hasBearerError(ctx) {
		return `Bearer error="invalid_token"`
	}
	return "Bearer"
}

// BearerGate resolves an Authorization: Bearer access token into a request-context
// Principal (token claims first, userinfo fallback). It skips when a Principal is
// already present (cookie wins) or no Bearer header is sent. An invalid token (or
// a token with no subject) marks the context (invalid_token) and proceeds
// unauthenticated. Tokens are never logged.
func BearerGate(v BearerVerifier, idCfg session.IdentityConfig, logger *slog.Logger) chain.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := session.FromContext(r.Context()); ok {
				next.ServeHTTP(w, r) // cookie already authenticated
				return
			}
			token, ok := parseBearer(r.Header.Get("Authorization"))
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			id, err := v.VerifyAccessToken(ctx, token)
			if err != nil || id == nil || id.Subject == "" {
				// invalid, or a token with no subject ⇒ no valid principal (fail-closed)
				r = r.WithContext(withBearerError(r.Context()))
				next.ServeHTTP(w, r)
				return
			}
			var userinfo map[string]any
			if session.NeedUserInfoForToken(idCfg, id.Claims) {
				if ui, uerr := v.UserInfo(ctx, token); uerr != nil {
					logger.Error("auth: bearer userinfo failed; proceeding token-only", "err", uerr) // never log the token
				} else {
					userinfo = ui
				}
			}
			p := session.NewPrincipal(id.Subject, id.Claims, userinfo, token, idCfg)
			r = r.WithContext(session.WithPrincipal(r.Context(), p))
			next.ServeHTTP(w, r)
		})
	})
}

// parseBearer extracts the token from an "Authorization: Bearer <token>" header
// (scheme is case-insensitive per RFC 7235). Returns false when absent/empty.
func parseBearer(h string) (string, bool) {
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
