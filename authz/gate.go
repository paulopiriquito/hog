package authz

import (
	"log/slog"
	"net/http"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/session"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Gate builds the authz middleware for a route: every policy in the effective
// set must be satisfied, else 403. An empty set is a pass-through (default-allow).
// The 403 body carries no policy detail; the reason is logged + recorded on the
// request span. routeName/labels are baked in for the policy input.
func Gate(policies []*Policy, routeName string, labels map[string]string, logger *slog.Logger) chain.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	if len(policies) == 0 {
		return chain.Func(func(next http.Handler) http.Handler { return next })
	}
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := session.FromContext(r.Context())
			input := buildInput(p, r, routeName, labels)
			for _, pol := range policies {
				deny, reason, err := pol.Decision(r.Context(), p, input)
				if !deny {
					continue
				}
				logDeny(r, logger, p, pol.Name, reason, err)
				span := trace.SpanFromContext(r.Context())
				span.AddEvent("authz.deny", trace.WithAttributes(
					attribute.String("authz.policy", pol.Name),
					attribute.String("authz.reason", reason)))
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

func logDeny(r *http.Request, logger *slog.Logger, p *session.Principal, policy, reason string, err error) {
	subject := ""
	if p != nil {
		subject = p.Subject
	}
	sc := trace.SpanContextFromContext(r.Context())
	attrs := []any{
		"policy", policy, "reason", reason,
		"subject", subject, "route", r.Pattern, "method", r.Method,
	}
	if sc.HasTraceID() {
		attrs = append(attrs, "trace_id", sc.TraceID().String())
	}
	if sc.HasSpanID() {
		attrs = append(attrs, "span_id", sc.SpanID().String())
	}
	if err != nil {
		logger.Error("authz denied (policy error)", append(attrs, "err", err)...)
	} else {
		logger.Info("authz denied", attrs...)
	}
}
