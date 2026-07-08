package session

import "strings"

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
