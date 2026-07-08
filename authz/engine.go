package authz

import (
	"context"
	"fmt"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/loader"
	"github.com/open-policy-agent/opa/rego"
)

// Engine is a compiled, prepared OPA query for one policy's Rego.
type Engine struct {
	query rego.PreparedEvalQuery
}

// denyRuleRef is the fully-qualified rule the engine queries and requires to
// be defined: data.hog.authz.deny.
var denyRuleRef = ast.MustParseRef("data.hog.authz.deny")

// NewEngine loads + compiles the .rego at path (a file or directory) and prepares
// the data.hog.authz.deny query. An empty path returns (nil, nil) — no engine.
//
// Fail-fast at Build (rather than fail-open at eval time): a load/parse/compile
// error is returned, a path with zero .rego modules is an error, and — critically
// — a rego that does not define a `deny` rule under `package hog.authz` (wrong
// package, typo'd rule name, `allow`-only policy, or an otherwise empty module)
// is rejected. Without this check, such a rego still compiles and prepares
// successfully, but every Eval call silently returns "no results" — which,
// upstream, is treated as an implicit allow — so a misconfigured/typo'd policy
// file would silently allow everything it should be denying.
func NewEngine(ctx context.Context, path string) (*Engine, error) {
	if path == "" {
		return nil, nil
	}
	loaded, err := loader.NewFileLoader().Filtered([]string{path}, nil)
	if err != nil {
		return nil, fmt.Errorf("load rego %q: %w", path, err)
	}
	modules := loaded.ParsedModules()
	if len(modules) == 0 {
		return nil, fmt.Errorf("rego %q: no .rego modules found", path)
	}
	compiler := ast.NewCompiler()
	compiler.Compile(modules)
	if compiler.Failed() {
		return nil, fmt.Errorf("compile rego %q: %w", path, compiler.Errors)
	}
	if len(compiler.GetRulesExact(denyRuleRef)) == 0 {
		return nil, fmt.Errorf("rego %q: no `deny` rule under `package hog.authz` (query data.hog.authz.deny is undefined)", path)
	}
	pq, err := rego.New(
		rego.Query("data.hog.authz.deny"),
		rego.Compiler(compiler),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare rego %q: %w", path, err)
	}
	return &Engine{query: pq}, nil
}

// Eval returns the deny-reason strings for input (empty ⇒ allow). Safe for
// concurrent use (the prepared query is immutable).
//
// If len(rs) == 0 or the result has no expressions, that means the query
// data.hog.authz.deny produced no result at all, which is only reachable for
// a valid config since NewEngine now rejects a rego that doesn't define
// `deny` — that's the "no results" allow path. But if the deny rule DOES
// exist and evaluates to a non-set value (e.g. `deny := true` or `deny := {}`
// written as an object instead of `deny contains msg if {...}`), this is a
// malformed policy at eval time; a security gate must fail CLOSED (return an
// error, which the caller treats as a deny) rather than silently allow.
func (e *Engine) Eval(ctx context.Context, input map[string]any) ([]string, error) {
	rs, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return nil, nil
	}
	set, ok := rs[0].Expressions[0].Value.([]any)
	if !ok {
		return nil, fmt.Errorf("authz: data.hog.authz.deny must be a set of strings, got %T (use `deny contains msg if { … }`)", rs[0].Expressions[0].Value)
	}
	// A non-empty deny set means the policy DID fire a deny. Stringify any
	// non-string member (an idiomatic `deny` yields strings, but a mistaken
	// `deny contains 42` still intends to deny) so the deny is never silently
	// dropped to an allow — fail closed.
	reasons := make([]string, 0, len(set))
	for _, v := range set {
		if s, ok := v.(string); ok {
			reasons = append(reasons, s)
		} else {
			reasons = append(reasons, fmt.Sprint(v))
		}
	}
	return reasons, nil
}
