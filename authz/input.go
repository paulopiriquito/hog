package authz

import (
	"net/http"

	"github.com/paulopiriquito/hog/session"
)

// buildInput assembles the policy input from the principal, request, and the
// route metadata baked into the gate at build time. It never includes the
// principal's AccessToken.
//
// subject/groups/claims are ALWAYS present in the returned map — present but
// empty for a nil principal (or a principal with nil Groups/Passport) — never
// omitted. A Rego rule that references e.g. `input.groups` must see a defined
// (if empty) value: an omitted key makes the reference undefined, which can
// make a `not "admins" in input.groups`-style deny silently fail to fire,
// letting anonymous/incomplete-principal requests through (fail open).
func buildInput(p *session.Principal, r *http.Request, routeName string, labels map[string]string) map[string]any {
	subject := ""
	groups := []string{}
	claims := map[string]any{}
	if p != nil {
		subject = p.Subject
		if p.Groups != nil {
			groups = p.Groups
		}
		if p.Passport != nil {
			claims = p.Passport
		}
	}
	if labels == nil {
		labels = map[string]string{}
	}
	return map[string]any{
		"subject": subject,
		"groups":  groups,
		"claims":  claims,
		"request": map[string]any{
			"method":     r.Method,
			"path":       r.URL.Path,
			"route":      r.Pattern,
			"route_name": routeName,
			"labels":     labels,
		},
	}
}
