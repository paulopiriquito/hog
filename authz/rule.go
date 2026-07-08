// Package authz is HOG's authorization layer: a built-in claim/group rule and an
// embedded OPA/Rego engine, composed into named policies referenced by routes.
// It is the only package that imports open-policy-agent/opa.
package authz

import (
	"fmt"

	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

// Require is the built-in (Tier A) claim/group rule.
type Require struct {
	Groups []string                `yaml:"groups"`
	Claims map[string]StringOrList `yaml:"claims"`
}

// StringOrList decodes a YAML scalar or sequence into a []string (any-of).
type StringOrList []string

// UnmarshalYAML accepts either `x: v` (scalar) or `x: [a, b]` (sequence).
func (s *StringOrList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var one string
		if err := node.Decode(&one); err != nil {
			return err
		}
		*s = StringOrList{one}
		return nil
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		return err
	}
	*s = many
	return nil
}

// Empty reports whether the require imposes no constraint.
func (r Require) Empty() bool { return len(r.Groups) == 0 && len(r.Claims) == 0 }

// Satisfied reports whether principal p satisfies r: (groups omitted OR p ∈ ≥1
// listed group) AND every claim constraint holds. A nil principal satisfies only
// an empty require (fail-closed).
func Satisfied(r Require, p *session.Principal) bool {
	if r.Empty() {
		return true
	}
	if p == nil {
		return false
	}
	if len(r.Groups) > 0 && !anyGroup(p.Groups, r.Groups) {
		return false
	}
	for claim, allowed := range r.Claims {
		v, ok := p.Passport[claim]
		if !ok || !containsStr(allowed, stringify(v)) {
			return false
		}
	}
	return true
}

func anyGroup(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, g := range have {
		set[g] = true
	}
	for _, w := range want {
		if set[w] {
			return true
		}
	}
	return false
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
