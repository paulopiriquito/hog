package authz

import (
	"context"
	"fmt"
	"strings"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/session"
)

// Policy is a compiled, named authorization unit: a built-in require rule and/or
// a Rego engine. Satisfied iff require passes (when present) AND rego deny is
// empty (when present).
type Policy struct {
	Name    string
	Require *Require
	Engine  *Engine
}

// policySpec is the decoded kind: Policy spec.
type policySpec struct {
	Require *Require `yaml:"require"`
	Rego    *struct {
		Path string `yaml:"path"`
	} `yaml:"rego"`
}

// Compile parses + compiles every kind: Policy resource into a name→*Policy map.
// Fail-fast: an empty policy (neither tier), an empty claim value list, a
// duplicate name, or a bad .rego is an error.
func Compile(ctx context.Context, resources []config.Resource) (map[string]*Policy, error) {
	out := make(map[string]*Policy, len(resources))
	for _, r := range resources {
		var spec policySpec
		if err := r.Spec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("policy %q: %w", r.Metadata.Name, err)
		}
		hasRego := spec.Rego != nil && spec.Rego.Path != ""
		if spec.Require != nil && spec.Require.Empty() {
			return nil, fmt.Errorf("policy %q: require block is empty (no groups or claims) — remove it or add constraints", r.Metadata.Name)
		}
		if spec.Require == nil && !hasRego {
			return nil, fmt.Errorf("policy %q: at least one of require/rego is required", r.Metadata.Name)
		}
		if spec.Require != nil {
			for claim, vals := range spec.Require.Claims {
				if len(vals) == 0 {
					return nil, fmt.Errorf("policy %q: claim %q has an empty value list (never satisfiable)", r.Metadata.Name, claim)
				}
			}
		}
		if _, dup := out[r.Metadata.Name]; dup {
			return nil, fmt.Errorf("duplicate policy %q", r.Metadata.Name)
		}
		p := &Policy{Name: r.Metadata.Name, Require: spec.Require}
		if hasRego {
			eng, err := NewEngine(ctx, spec.Rego.Path)
			if err != nil {
				return nil, fmt.Errorf("policy %q: %w", r.Metadata.Name, err)
			}
			p.Engine = eng
		}
		out[r.Metadata.Name] = p
	}
	return out, nil
}

// Decision evaluates the policy. deny=true with a reason when the require is unmet
// or the rego produces a deny; err != nil signals a rego eval failure (the caller
// treats it as a fail-closed deny and logs it at Error).
func (p *Policy) Decision(ctx context.Context, principal *session.Principal, input map[string]any) (deny bool, reason string, err error) {
	if p.Require != nil && !Satisfied(*p.Require, principal) {
		return true, "require not satisfied", nil
	}
	if p.Engine != nil {
		reasons, e := p.Engine.Eval(ctx, input)
		if e != nil {
			return true, "policy evaluation error", e
		}
		if len(reasons) > 0 {
			return true, strings.Join(reasons, "; "), nil
		}
	}
	return false, "", nil
}
