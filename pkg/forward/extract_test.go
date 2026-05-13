package forward_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/pkg/forward"
)

func TestApply_ScalarClaim_NoMapping_PassesThrough(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "sub", Name: "X-User-Id"},
	}}
	userinfo := map[string]any{"sub": "90019066"}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-User-Id"], "90019066"; got != want {
		t.Errorf("headers: got %q, want %q", got, want)
	}
	if got, want := res.Mapped["X-User-Id"], "90019066"; got != want {
		t.Errorf("mapped: got %v, want %q", got, want)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("expected no diagnostics, got %v", res.Diagnostics)
	}
}

func TestApply_NumericScalarStringifies(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "employeeNumber", Name: "X-Emp"},
	}}
	userinfo := map[string]any{"employeeNumber": float64(14947156)}

	res := forward.Apply(userinfo, cfg)

	if got := res.Headers["X-Emp"]; got != "14947156" {
		t.Errorf("got %q, want %q", got, "14947156")
	}
}

func TestApply_ArrayClaim_NoMapping_JoinsCommaSeparated(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "groups", Name: "X-Groups"},
	}}
	userinfo := map[string]any{"groups": []any{"admin", "user"}}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-Groups"], "admin,user"; got != want {
		t.Errorf("headers: got %q, want %q", got, want)
	}
	wantMapped := []string{"admin", "user"}
	if got, ok := res.Mapped["X-Groups"].([]string); !ok || !reflect.DeepEqual(got, wantMapped) {
		t.Errorf("mapped: got %v, want %v", res.Mapped["X-Groups"], wantMapped)
	}
}

func TestApply_MissingClaim_OmitsHeaderAndEmitsDiagnostic(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "nope", Name: "X-Nope"},
	}}
	userinfo := map[string]any{"sub": "x"}

	res := forward.Apply(userinfo, cfg)

	if _, present := res.Headers["X-Nope"]; present {
		t.Errorf("expected header omitted")
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != "missing_claim" {
		t.Errorf("expected one missing_claim diagnostic, got %v", res.Diagnostics)
	}
}

func TestApply_WrongType_OmitsHeaderAndEmitsDiagnostic(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "obj", Name: "X-Obj"},
	}}
	userinfo := map[string]any{"obj": map[string]any{"nested": "x"}}

	res := forward.Apply(userinfo, cfg)

	if _, present := res.Headers["X-Obj"]; present {
		t.Errorf("expected header omitted")
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != "wrong_type" {
		t.Errorf("expected one wrong_type diagnostic, got %v", res.Diagnostics)
	}
}

func TestApply_ScalarClaim_MappingMatch(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "role", Name: "X-Role", Mapping: []forward.Rule{
			{From: "admin", To: "ADMIN"},
		}},
	}}
	userinfo := map[string]any{"role": "site-admin"}

	res := forward.Apply(userinfo, cfg)

	if got := res.Headers["X-Role"]; got != "ADMIN" {
		t.Errorf("got %q, want %q", got, "ADMIN")
	}
}

func TestApply_ScalarClaim_MappingNoMatch_OmitsHeader(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "role", Name: "X-Role", Mapping: []forward.Rule{
			{From: "admin", To: "ADMIN"},
		}},
	}}
	userinfo := map[string]any{"role": "guest"}

	res := forward.Apply(userinfo, cfg)

	if _, present := res.Headers["X-Role"]; present {
		t.Errorf("expected header omitted")
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != "no_matches" {
		t.Errorf("expected no_matches diagnostic, got %v", res.Diagnostics)
	}
}

func TestApply_ArrayClaim_MappingFiltersRenamesDedups(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-User-Roles", Mapping: []forward.Rule{
			{From: "cn=KRONOS,", To: "KRONOS-USER"},
			{From: "cn=GITHUB,", To: "GITHUB-MEMBER"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{
		"cn=KRONOS,ou=app,o=corp",
		"cn=NOISE,ou=mail,o=corp", // unmatched, dropped
		"cn=GITHUB,ou=app,o=corp",
		"cn=KRONOS,ou=app,o=corp", // duplicate, dedup
	}}

	res := forward.Apply(userinfo, cfg)

	want := "KRONOS-USER,GITHUB-MEMBER"
	if got := res.Headers["X-User-Roles"]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	mapped, ok := res.Mapped["X-User-Roles"].([]string)
	if !ok {
		t.Fatalf("mapped not []string: %v", res.Mapped["X-User-Roles"])
	}
	if !reflect.DeepEqual(mapped, []string{"KRONOS-USER", "GITHUB-MEMBER"}) {
		t.Errorf("mapped got %v", mapped)
	}
}

func TestApply_FirstMatchWinsPerValue(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{
			{From: "cn=PT-LM-ROLE-invoice-portal-search_invoices,", To: "INVOICE-SEARCH"},
			{From: "cn=PT-LM-ROLE-invoice-portal-", To: "INVOICE-GENERIC"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{
		"cn=PT-LM-ROLE-invoice-portal-search_invoices,ou=app",
	}}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-Roles"], "INVOICE-SEARCH"; got != want {
		t.Errorf("got %q, want %q (first rule should win)", got, want)
	}
}

func TestApply_ArrayClaim_AllUnmatched_OmitsHeaderWithSamples(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{
			{From: "cn=ADMIN,", To: "ADMIN"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{
		"cn=USER1,o=corp",
		"cn=USER2,o=corp",
		"cn=USER3,o=corp",
		"cn=USER4,o=corp", // sample limit is 3
	}}

	res := forward.Apply(userinfo, cfg)

	if _, present := res.Headers["X-Roles"]; present {
		t.Errorf("expected header omitted")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Reason != "no_matches" {
		t.Errorf("reason: got %q, want no_matches", d.Reason)
	}
	if len(d.Samples) != 3 {
		t.Errorf("samples len: got %d, want 3", len(d.Samples))
	}
}

func TestApply_TruncatesLongSampleValues(t *testing.T) {
	long := strings.Repeat("x", 200)
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{
			{From: "match-nothing", To: "X"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{long}}

	res := forward.Apply(userinfo, cfg)

	if len(res.Diagnostics) != 1 || len(res.Diagnostics[0].Samples) != 1 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	got := res.Diagnostics[0].Samples[0]
	if len(got) > 83 { // 80 + "..."
		t.Errorf("sample not truncated: len=%d", len(got))
	}
}

func TestApply_FractionalFloatStringifiesWithoutScientificNotation(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "ratio", Name: "X-Ratio"},
	}}
	userinfo := map[string]any{"ratio": 3.14159}

	res := forward.Apply(userinfo, cfg)

	if got := res.Headers["X-Ratio"]; got != "3.14159" {
		t.Errorf("got %q, want %q", got, "3.14159")
	}
}
