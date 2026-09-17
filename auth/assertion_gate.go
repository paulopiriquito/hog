package auth

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/paulopiriquito/hog/v2/chain"
	"github.com/paulopiriquito/hog/v2/session"
)

type assertionHeaderKey struct{}

// AssertionHeaderFromContext returns the header name IssueAssertion minted (or
// attempted to mint) the identity assertion under on this request, or "" if
// IssueAssertion did not run for it. A terminal handler's forward logic uses
// this to learn the name: it never sees the gateway's identity config
// directly, only the per-route forwardIdentity flag.
func AssertionHeaderFromContext(ctx context.Context) string {
	h, _ := ctx.Value(assertionHeaderKey{}).(string)
	return h
}

// AssertionGate accepts an identity assertion from a peer HOG instance. It always
// removes the header from the request, so a backend never sees it. With a valid
// assertion whose subject matches the principal the Bearer gate already resolved,
// the principal is enriched with the asserted passport and groups, which saves a
// second userinfo round trip. Without a principal the assertion authenticates on
// its own only when requireBearer is false. Invalid or mismatching assertions are
// logged and ignored — this gate never rejects a request by itself.
func AssertionGate(v *AssertionVerifier, header string, requireBearer bool, logger *slog.Logger) chain.Middleware {
	if header == "" {
		header = DefaultAssertionHeader
	}
	if logger == nil {
		logger = slog.Default()
	}
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get(header)
			// Removed unconditionally, before anything else: a backend must never see
			// an inbound assertion, valid or not.
			r.Header.Del(header)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			a, err := v.Verify(token)
			if err != nil {
				logger.Info("identity assertion: rejected; proceeding without enrichment", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			p, hasPrincipal := session.FromContext(r.Context())
			switch {
			case hasPrincipal && p.Subject == a.Subject:
				enriched := &session.Principal{
					Subject:     p.Subject,
					Passport:    a.Passport,
					Groups:      a.Groups,
					AccessToken: p.AccessToken,
					SessionID:   p.SessionID,
				}
				r = r.WithContext(session.WithPrincipal(r.Context(), enriched))
			case hasPrincipal:
				logger.Warn("identity assertion: subject mismatch; ignoring", "asserted_subject", a.Subject)
			case !requireBearer:
				r = r.WithContext(session.WithPrincipal(r.Context(), &session.Principal{
					Subject:  a.Subject,
					Passport: a.Passport,
					Groups:   a.Groups,
				}))
			default:
				logger.Info("identity assertion: no bearer-authenticated principal to enrich; ignoring (requireBearer=true)")
			}
			next.ServeHTTP(w, r)
		})
	})
}

// IssueAssertion removes any inbound assertion header (a client must never be able
// to supply one) and, when a principal is present, sets a freshly minted assertion
// for the backend hop.
func IssueAssertion(iss *AssertionIssuer, header string, logger *slog.Logger) chain.Middleware {
	if header == "" {
		header = DefaultAssertionHeader
	}
	if logger == nil {
		logger = slog.Default()
	}
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del(header)
			// Recorded regardless of whether a principal is present below: the
			// terminal handler's forward/strip logic needs the name either way,
			// since a backend hop can be configured to strip this same header.
			r = r.WithContext(context.WithValue(r.Context(), assertionHeaderKey{}, header))
			if p, ok := session.FromContext(r.Context()); ok {
				token, err := iss.Mint(p)
				if err != nil {
					// Never fail the request over a minting error: the backend simply
					// proceeds without an assertion, same as an unauthenticated request.
					logger.Error("identity assertion: mint failed; proceeding without one", "err", err)
				} else {
					r.Header.Set(header, token)
				}
			}
			next.ServeHTTP(w, r)
		})
	})
}
