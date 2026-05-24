package forward_test

import (
	"reflect"
	"testing"

	"github.com/paulopiriquito/hog/pkg/forward"
)

func TestResolve_TopLevelString(t *testing.T) {
	userinfo := map[string]any{"sub": "90019066"}
	got, ok := forward.Resolve(userinfo, "sub")
	if !ok || got != "90019066" {
		t.Fatalf("got (%v, %v), want (\"90019066\", true)", got, ok)
	}
}

func TestResolve_TopLevelArray(t *testing.T) {
	userinfo := map[string]any{"groups": []any{"a", "b"}}
	got, ok := forward.Resolve(userinfo, "groups")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := []any{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolve_DottedPath(t *testing.T) {
	userinfo := map[string]any{
		"realm_access": map[string]any{
			"roles": []any{"admin"},
		},
	}
	got, ok := forward.Resolve(userinfo, "realm_access.roles")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := []any{"admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolve_MissingSegmentReturnsNotOk(t *testing.T) {
	userinfo := map[string]any{"sub": "x"}
	_, ok := forward.Resolve(userinfo, "missing")
	if ok {
		t.Fatalf("expected ok=false for missing top-level key")
	}
}

func TestResolve_TraversingNonMapReturnsNotOk(t *testing.T) {
	userinfo := map[string]any{"sub": "x"}
	_, ok := forward.Resolve(userinfo, "sub.deeper")
	if ok {
		t.Fatalf("expected ok=false when traversing through a scalar")
	}
}

func TestResolve_EmptyPathReturnsNotOk(t *testing.T) {
	userinfo := map[string]any{"sub": "x"}
	_, ok := forward.Resolve(userinfo, "")
	if ok {
		t.Fatalf("expected ok=false for empty path")
	}
}
