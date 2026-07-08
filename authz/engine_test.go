package authz

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeRego(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.rego"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const denyDelete = `package hog.authz
import rego.v1

deny contains msg if {
	input.request.method == "DELETE"
	not "admins" in input.groups
	msg := "DELETE requires admins"
}
`

func TestEngineDenyAndAllow(t *testing.T) {
	eng, err := NewEngine(context.Background(), writeRego(t, denyDelete))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	reasons, err := eng.Eval(context.Background(), map[string]any{
		"groups": []string{"users"}, "request": map[string]any{"method": "DELETE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 1 || reasons[0] != "DELETE requires admins" {
		t.Fatalf("deny reasons = %v", reasons)
	}
	reasons, err = eng.Eval(context.Background(), map[string]any{
		"groups": []string{"users"}, "request": map[string]any{"method": "GET"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("expected allow, got deny %v", reasons)
	}
}

func TestEngineEmptyPathNilEngine(t *testing.T) {
	eng, err := NewEngine(context.Background(), "")
	if err != nil || eng != nil {
		t.Fatalf("empty path ⇒ (nil,nil), got (%v,%v)", eng, err)
	}
}

func TestEngineBadRego(t *testing.T) {
	if _, err := NewEngine(context.Background(), writeRego(t, "package hog.authz\nthis is not rego")); err == nil {
		t.Fatal("want compile error for bad rego")
	}
}

// TestEngineWrongPackageRejected is the I2 regression: a deny rule defined
// under the wrong package compiles fine on its own, but data.hog.authz.deny
// is then undefined, so every Eval would silently return "no results" (⇒
// upstream allow). NewEngine must reject this at load time.
func TestEngineWrongPackageRejected(t *testing.T) {
	const wrongPackage = `package hog.notauthz
import rego.v1

deny contains msg if {
	input.request.method == "DELETE"
	msg := "nope"
}
`
	if _, err := NewEngine(context.Background(), writeRego(t, wrongPackage)); err == nil {
		t.Fatal("deny rule under the wrong package ⇒ NewEngine error (data.hog.authz.deny undefined)")
	}
}

// TestEngineAllowOnlyRejected is the I2 regression for a rego that defines
// something (e.g. an `allow` rule) under the right package, but never a
// `deny` rule — data.hog.authz.deny is still undefined.
func TestEngineAllowOnlyRejected(t *testing.T) {
	const allowOnly = `package hog.authz
import rego.v1

allow if {
	input.request.method == "GET"
}
`
	if _, err := NewEngine(context.Background(), writeRego(t, allowOnly)); err == nil {
		t.Fatal("rego with no `deny` rule ⇒ NewEngine error (data.hog.authz.deny undefined)")
	}
}

// TestEngineEmptyDirRejected is the I2 regression for a path with zero .rego
// modules (e.g. an empty directory, or one containing only non-.rego files) —
// there is nothing to define `deny`, so it must be rejected rather than
// silently producing a no-op (always-allow) engine.
func TestEngineEmptyDirRejected(t *testing.T) {
	dir := t.TempDir() // no files written
	if _, err := NewEngine(context.Background(), dir); err == nil {
		t.Fatal("empty dir (no .rego modules) ⇒ NewEngine error")
	}
}

// TestEngineBooleanDenyFailsClosed is the I3 regression: an operator writes
// `deny := true` (or any non-set value) instead of the expected
// `deny contains msg if {...}` partial-set rule. data.hog.authz.deny IS
// defined, so NewEngine's deny-rule-exists check passes, but the runtime
// shape is wrong. Eval must fail closed (return an error) rather than
// silently treat the unexpected shape as "no denies ⇒ allow".
func TestEngineBooleanDenyFailsClosed(t *testing.T) {
	const booleanDeny = `package hog.authz

deny := true
`
	eng, err := NewEngine(context.Background(), writeRego(t, booleanDeny))
	if err != nil {
		// Also acceptable: rejected at load time. Either way, the malformed
		// shape must never silently evaluate to allow.
		return
	}
	if _, err := eng.Eval(context.Background(), map[string]any{
		"groups": []string{}, "request": map[string]any{"method": "DELETE"},
	}); err == nil {
		t.Fatal("deny := true (not a set) ⇒ Eval must fail closed (return an error), not silently allow")
	}
}

// TestEngineObjectDenyFailsClosed mirrors TestEngineBooleanDenyFailsClosed
// for a `deny` rule that evaluates to an object instead of a set.
func TestEngineObjectDenyFailsClosed(t *testing.T) {
	const objectDeny = `package hog.authz

deny := {"reason": "always"}
`
	eng, err := NewEngine(context.Background(), writeRego(t, objectDeny))
	if err != nil {
		return
	}
	if _, err := eng.Eval(context.Background(), map[string]any{
		"groups": []string{}, "request": map[string]any{"method": "DELETE"},
	}); err == nil {
		t.Fatal("deny as an object (not a set) ⇒ Eval must fail closed (return an error), not silently allow")
	}
}

// A deny set with a non-string member (e.g. `deny contains 42`) still FIRED a
// deny in Rego; Eval must surface it as a reason (deny), never drop it to allow.
func TestEngineNonStringDenyMemberFiresDeny(t *testing.T) {
	const numericDeny = `package hog.authz
import rego.v1

deny contains n if {
	input.request.method == "DELETE"
	n := 42
}
`
	eng, err := NewEngine(context.Background(), writeRego(t, numericDeny))
	if err != nil {
		t.Fatal(err)
	}
	reasons, err := eng.Eval(context.Background(), map[string]any{
		"groups": []string{}, "request": map[string]any{"method": "DELETE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 1 {
		t.Fatalf("non-string deny member must fire a deny (fail closed), got reasons=%v", reasons)
	}
}
