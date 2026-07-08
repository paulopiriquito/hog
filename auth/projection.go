package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/route"
	"github.com/paulopiriquito/hog/session"
)

// ProjectionGate strips inbound X-User-* headers (anti-spoof) and, when a
// principal is present, injects identity headers for the backend. proj nil =>
// derive from the passport+groups; groupsAs is the session Groups.As (default
// "groups") used to name the groups header when not overridden.
func ProjectionGate(proj *route.ProjectionConfig, groupsAs string) chain.Middleware {
	if groupsAs == "" {
		groupsAs = "groups"
	}
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripUserHeaders(r)
			if p, ok := session.FromContext(r.Context()); ok {
				applyProjection(r, p, proj, groupsAs)
			}
			next.ServeHTTP(w, r)
		})
	})
}

// stripUserHeaders deletes every inbound X-User-* request header.
// Collect-then-delete form is used to avoid any ambiguity about
// deleting from a map during iteration.
// Underscore variants (e.g. X_User_Id) are the upstream proxy's responsibility to normalize or reject; this gate trusts canonical dash-form header names.
func stripUserHeaders(r *http.Request) {
	var drop []string
	for name := range r.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "X-User-") {
			drop = append(drop, name)
		}
	}
	for _, name := range drop {
		r.Header.Del(name)
	}
}

func applyProjection(r *http.Request, p *session.Principal, proj *route.ProjectionConfig, groupsAs string) {
	sp := projectionSession(proj)

	groupsHeader := "X-User-" + canon(groupsAs)
	if sp != nil && sp.Groups != nil && sp.Groups.Header != "" {
		groupsHeader = sp.Groups.Header
	}

	if sp != nil && len(sp.Claims) > 0 {
		// Override mode: clear every configured target header first (anti-spoof —
		// these may live outside the X-User-* namespace that stripUserHeaders
		// covers), then project only the listed claims that the passport carries.
		for _, header := range sp.Claims {
			r.Header.Del(header)
		}
		for claim, header := range sp.Claims {
			if v, ok := p.Passport[claim]; ok {
				setHeaderSafe(r, header, stringify(v))
			}
		}
	} else {
		// Derive mode: one X-User-<claim> per scalar passport claim. Skip claims
		// whose derived header would collide with the reserved subject/groups
		// headers (written below, authoritatively) or whose canon is empty, and
		// skip non-scalar values (arrays/objects never project sensibly).
		for k, v := range p.Passport {
			c := canon(k)
			if c == "" {
				continue
			}
			header := "X-User-" + c
			if header == "X-User-Id" || header == groupsHeader {
				continue
			}
			if !isScalar(v) {
				continue
			}
			setHeaderSafe(r, header, stringify(v))
		}
	}

	// Trusted, un-clobberable values, written last so no claim can override them.
	setHeaderSafe(r, "X-User-Id", p.Subject)

	// Groups header: always clear any inbound spoof; set only when groups exist.
	r.Header.Del(groupsHeader)
	if len(p.Groups) > 0 {
		setHeaderSafe(r, groupsHeader, strings.Join(p.Groups, ","))
	}
}

// setHeaderSafe sets a request header unless the name is not a valid header
// token or the value contains a control character (CR/LF/NUL/DEL etc.) — either
// of which could corrupt or smuggle headers on a downstream forward. Such
// writes are dropped (fail closed).
func setHeaderSafe(r *http.Request, name, value string) {
	if name == "" || strings.IndexFunc(name, invalidHeaderNameChar) >= 0 {
		return
	}
	if strings.IndexFunc(value, func(c rune) bool { return c < 0x20 || c == 0x7f }) >= 0 {
		return
	}
	r.Header.Set(name, value)
}

// invalidHeaderNameChar reports whether c is not a valid RFC 7230 header-name
// (token) character. Header names derive from operator config; an invalid one
// is dropped rather than emitted as a malformed header.
func invalidHeaderNameChar(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	case strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		return false
	default:
		return true
	}
}

// isScalar reports whether v projects sensibly to a single header value.
// Arrays, objects, and nil are skipped in derive mode (groups have their own
// dedicated header). Scalars (string/number/bool) project via stringify.
func isScalar(v any) bool {
	switch v.(type) {
	case nil, []any, []string, map[string]any:
		return false
	default:
		return true
	}
}

func projectionSession(proj *route.ProjectionConfig) *route.SessionProjection {
	if proj == nil {
		return nil
	}
	return proj.Session
}

// canon turns a claim name into a header token: split on "_" and "-",
// Title-Case each segment (first letter upper, rest lower), join with "-".
// Examples: "email" => "Email", "given_name" => "Given-Name",
// "preferred-username" => "Preferred-Username".
func canon(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, "-")
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
