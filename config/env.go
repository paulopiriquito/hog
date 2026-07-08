package config

import (
	"fmt"
	"strings"
)

// ExpandEnv replaces ${VAR} and ${VAR:-default} in s using lookup.
// A ${VAR} with no value and no default is an error. An empty (but set)
// value satisfies the variable and suppresses any default.
func ExpandEnv(s string, lookup func(string) (string, bool)) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		b.WriteString(s[:i])
		end := strings.Index(s[i:], "}")
		if end < 0 {
			return "", fmt.Errorf("unterminated ${ in config")
		}
		expr := s[i+2 : i+end] // contents between ${ and }
		s = s[i+end+1:]

		name, def, hasDef := expr, "", false
		if j := strings.Index(expr, ":-"); j >= 0 {
			name, def, hasDef = expr[:j], expr[j+2:], true
		}
		if v, ok := lookup(name); ok {
			b.WriteString(v)
		} else if hasDef {
			b.WriteString(def)
		} else {
			return "", fmt.Errorf("required env var %q is not set", name)
		}
	}
}
