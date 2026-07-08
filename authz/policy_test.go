package authz

import (
	"context"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/session"
)

func TestPolicyRequireOnly(t *testing.T) {
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [admins] }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := pols["p"]
	if pol == nil || pol.Require == nil || pol.Engine != nil {
		t.Fatalf("policy = %+v", pol)
	}

	deny, reason, err := pol.Decision(context.Background(), &session.Principal{Subject: "u", Groups: []string{"users"}}, nil)
	if err != nil || !deny || reason == "" {
		t.Fatalf("unsatisfied require: deny=%v reason=%q err=%v", deny, reason, err)
	}

	deny, _, err = pol.Decision(context.Background(), &session.Principal{Subject: "u", Groups: []string{"admins"}}, nil)
	if err != nil || deny {
		t.Fatalf("satisfied require: deny=%v err=%v", deny, err)
	}
}

func TestPolicyRegoOnly(t *testing.T) {
	dir := writeRego(t, denyDelete)
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  rego: { path: "+dir+" }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := pols["p"]
	if pol == nil || pol.Require != nil || pol.Engine == nil {
		t.Fatalf("policy = %+v", pol)
	}

	deny, reason, err := pol.Decision(context.Background(), nil, map[string]any{
		"groups": []string{"users"}, "request": map[string]any{"method": "DELETE"},
	})
	if err != nil || !deny || !strings.Contains(reason, "DELETE") {
		t.Fatalf("rego deny: deny=%v reason=%q err=%v", deny, reason, err)
	}

	deny, _, err = pol.Decision(context.Background(), nil, map[string]any{
		"groups": []string{"users"}, "request": map[string]any{"method": "GET"},
	})
	if err != nil || deny {
		t.Fatalf("rego allow: deny=%v err=%v", deny, err)
	}
}

// TestPolicyBothMustPass: when a policy carries both tiers, a satisfied require
// does NOT short-circuit a denying rego — both must pass for an allow.
func TestPolicyBothMustPass(t *testing.T) {
	dir := writeRego(t, denyDelete)
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [users] }\n  rego: { path: "+dir+" }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := pols["p"]
	if pol.Require == nil || pol.Engine == nil {
		t.Fatalf("expected both tiers present: %+v", pol)
	}

	principal := &session.Principal{Subject: "u", Groups: []string{"users"}} // satisfies require; not "admins" ⇒ rego denies DELETE
	deny, reason, err := pol.Decision(context.Background(), principal, map[string]any{
		"groups": principal.Groups, "request": map[string]any{"method": "DELETE"},
	})
	if err != nil || !deny {
		t.Fatalf("satisfied require + denying rego must still deny: deny=%v err=%v", deny, err)
	}
	if !strings.Contains(reason, "DELETE") {
		t.Fatalf("expected rego deny reason, got %q", reason)
	}

	// Both tiers pass ⇒ allow.
	deny, _, err = pol.Decision(context.Background(), principal, map[string]any{
		"groups": principal.Groups, "request": map[string]any{"method": "GET"},
	})
	if err != nil || deny {
		t.Fatalf("satisfied require + non-denying rego must allow: deny=%v err=%v", deny, err)
	}

	// Require unmet short-circuits before rego is even consulted.
	deny, reason, err = pol.Decision(context.Background(), &session.Principal{Subject: "u2", Groups: []string{"nobody"}}, map[string]any{
		"groups": []string{"nobody"}, "request": map[string]any{"method": "GET"},
	})
	if err != nil || !deny || reason != "require not satisfied" {
		t.Fatalf("unsatisfied require must deny regardless of rego: deny=%v reason=%q err=%v", deny, reason, err)
	}
}

func TestCompileBuildsNameMap(t *testing.T) {
	pols, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { groups: [a] }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 1 || pols["p"] == nil || pols["p"].Name != "p" {
		t.Fatalf("compiled map = %+v", pols)
	}
}

func TestCompileRejectsDuplicateName(t *testing.T) {
	rs, err := config.DecodeAll([]byte(
		"kind: Policy\nmetadata: { name: dup }\nspec:\n  require: { groups: [a] }\n" +
			"---\n" +
			"kind: Policy\nmetadata: { name: dup }\nspec:\n  require: { groups: [b] }\n"))
	if err != nil || len(rs) != 2 {
		t.Fatalf("decode: %v (n=%d)", err, len(rs))
	}
	if _, err := Compile(context.Background(), rs); err == nil {
		t.Fatal("duplicate policy name ⇒ error")
	}
}

func TestCompileRejectsEmptyClaimList(t *testing.T) {
	if _, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { claims: { tier: [] } }\n"),
	}); err == nil {
		t.Fatal("empty claim value list ⇒ error (never satisfiable)")
	}
}

// TestCompileRejectsEmptyRequireBlock is the I1 regression: a present-but-empty
// `require: {}` decodes to a non-nil *Require with Empty()==true. Before the
// fix, Compile only rejected spec.Require == nil, so this slipped through and
// Satisfied(Require{}, p) unconditionally returns true — the policy allows
// every request regardless of principal.
func TestCompileRejectsEmptyRequireBlock(t *testing.T) {
	if _, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: {}\n"),
	}); err == nil {
		t.Fatal("empty require block ⇒ error (would always-allow)")
	}
}

// TestCompileRejectsTypoedRequireField is the I1 regression via a realistic
// misconfiguration: `group:` (singular) instead of `groups:`. The YAML decoder
// silently ignores the unknown field, leaving Require.Groups nil/empty — same
// always-allow hazard as an explicit `require: {}`, caught by the same
// Empty() check.
func TestCompileRejectsTypoedRequireField(t *testing.T) {
	if _, err := Compile(context.Background(), []config.Resource{
		policyResource(t, "  require: { group: [admins] }\n"),
	}); err == nil {
		t.Fatal("require with only an unrecognized field (typo) ⇒ error (Groups/Claims stay empty ⇒ would always-allow)")
	}
}
