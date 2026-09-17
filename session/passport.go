package session

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// projectPassport copies allowlisted claims, preferring userinfo over id_token.
// Absent claims are skipped. sub is handled separately by the caller.
func projectPassport(claims []string, idClaims, userinfo map[string]any) map[string]any {
	out := make(map[string]any, len(claims))
	for _, name := range claims {
		if v, ok := userinfo[name]; ok {
			out[name] = v
		} else if v, ok := idClaims[name]; ok {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// projectGroups filters the group-DN list to entries matching any configured
// pattern (case-insensitive substring) and renders each as its cn= value or
// the whole DN. userinfo is preferred; idClaims is the fallback (Bearer
// tokens carry group claims directly). Order is preserved; results are deduped.
func projectGroups(cfg *GroupsConfig, userinfo, idClaims map[string]any) []string {
	if cfg == nil {
		return nil
	}
	raw, ok := userinfo[cfg.Source]
	if !ok {
		raw, ok = idClaims[cfg.Source]
	}
	if !ok {
		return nil
	}
	dns := toStringSlice(raw)
	var out []string
	seen := map[string]bool{}
	for _, dn := range dns {
		if !matchesAny(dn, cfg.Match) {
			continue
		}
		v := dn
		if cfg.Render == "cn" {
			v = extractCN(dn)
		}
		v = stripPrefixes(v, cfg.Strip)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func matchesAny(dn string, patterns []string) bool {
	low := strings.ToLower(dn)
	for _, p := range patterns {
		if strings.Contains(low, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// extractCN returns the first cn= component's value (e.g. "cn=Foo,ou=x" → "Foo").
func extractCN(dn string) string {
	for _, part := range strings.Split(dn, ",") {
		part = strings.TrimSpace(part)
		if len(part) > 3 && strings.EqualFold(part[:3], "cn=") {
			return part[3:]
		}
	}
	return ""
}

// stripPrefixes removes the first configured prefix that v starts with
// (case-insensitive); an empty list is a no-op.
func stripPrefixes(v string, prefixes []string) string {
	if len(prefixes) == 0 {
		return v
	}
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if n, ok := foldPrefixLen(v, p); ok {
			return v[n:]
		}
	}
	return v
}

// foldPrefixLen reports how many bytes of v are matched by the prefix p under
// case-insensitive comparison. The two byte lengths can differ — case folding is
// not length-preserving in Unicode — so both are walked rune by rune instead of
// slicing v at len(p), which would cut inside a rune.
func foldPrefixLen(v, p string) (int, bool) {
	i, j := 0, 0
	for j < len(p) {
		if i >= len(v) {
			return 0, false
		}
		rv, nv := utf8.DecodeRuneInString(v[i:])
		rp, np := utf8.DecodeRuneInString(p[j:])
		if unicode.ToLower(rv) != unicode.ToLower(rp) {
			return 0, false
		}
		i += nv
		j += np
	}
	return i, true
}

// resolveSubject picks the principal's subject: the configured claim (userinfo
// first, then the token), or fallback (the token's own sub) when the claim is
// "sub", empty, absent or not a string — a subject is never silently emptied.
func resolveSubject(claim, fallback string, idClaims, userinfo map[string]any) string {
	if claim == "" || claim == "sub" {
		return fallback
	}
	if v, ok := userinfo[claim].(string); ok && v != "" {
		return v
	}
	if v, ok := idClaims[claim].(string); ok && v != "" {
		return v
	}
	return fallback
}

// toStringSlice coerces a userinfo claim value to []string ([]any or []string).
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
