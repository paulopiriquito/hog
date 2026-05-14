package forward_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/pkg/forward"
)

func TestApply_ScalarClaim_NoMapping_PassesThrough_HeaderOnly(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "sub", Name: "X-User-Id"},
	}}
	userinfo := map[string]any{"sub": "90019066"}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-User-Id"], "90019066"; got != want {
		t.Errorf("headers: got %q, want %q", got, want)
	}
	if len(res.Mapped) != 0 {
		t.Errorf("expected empty Mapped (no As set), got %v", res.Mapped)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("expected no diagnostics, got %v", res.Diagnostics)
	}
}

func TestApply_ScalarClaim_WithAs_PublishesToMapped(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "sub", Name: "X-User-Id", As: "userId"},
	}}
	userinfo := map[string]any{"sub": "90019066"}

	res := forward.Apply(userinfo, cfg)

	if got := res.Headers["X-User-Id"]; got != "90019066" {
		t.Errorf("headers: got %q, want %q", got, "90019066")
	}
	if got := res.Mapped["userId"]; got != "90019066" {
		t.Errorf("mapped[\"userId\"]: got %v, want %q", got, "90019066")
	}
	if _, present := res.Mapped["X-User-Id"]; present {
		t.Errorf("Mapped must be keyed by As, not by HTTP header name")
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

func TestApply_ArrayClaim_NoMapping_JoinsCommaSeparated_WithAs(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "groups", Name: "X-Groups", As: "groups"},
	}}
	userinfo := map[string]any{"groups": []any{"admin", "user"}}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-Groups"], "admin,user"; got != want {
		t.Errorf("headers: got %q, want %q", got, want)
	}
	wantMapped := []string{"admin", "user"}
	if got, ok := res.Mapped["groups"].([]string); !ok || !reflect.DeepEqual(got, wantMapped) {
		t.Errorf("mapped[\"groups\"]: got %v, want %v", res.Mapped["groups"], wantMapped)
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
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != forward.ReasonMissingClaim {
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
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != forward.ReasonWrongType {
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
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Reason != forward.ReasonNoMatches {
		t.Errorf("expected no_matches diagnostic, got %v", res.Diagnostics)
	}
}

func TestApply_ArrayClaim_MappingFiltersRenamesDedups(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-User-Roles", As: "roles", Mapping: []forward.Rule{
			{From: "cn=APP-A,", To: "APP-A-USER"},
			{From: "cn=APP-B,", To: "APP-B-USER"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{
		"cn=APP-A,ou=app,o=corp",
		"cn=NOISE,ou=mail,o=corp", // unmatched, dropped
		"cn=APP-B,ou=app,o=corp",
		"cn=APP-A,ou=app,o=corp", // duplicate, dedup
	}}

	res := forward.Apply(userinfo, cfg)

	want := "APP-A-USER,APP-B-USER"
	if got := res.Headers["X-User-Roles"]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	mapped, ok := res.Mapped["roles"].([]string)
	if !ok {
		t.Fatalf("mapped[\"roles\"] not []string: %v", res.Mapped["roles"])
	}
	if !reflect.DeepEqual(mapped, []string{"APP-A-USER", "APP-B-USER"}) {
		t.Errorf("mapped got %v", mapped)
	}
}

func TestApply_FirstMatchWinsPerValue(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{
			{From: "cn=PT-XP-ROLE-APP-B-SEARCH,", To: "APP-B-SEARCH"},
			{From: "cn=PT-XP-ROLE-APP-B-", To: "APP-B-GENERIC"},
		}},
	}}
	userinfo := map[string]any{"memberof": []any{
		"cn=PT-XP-ROLE-APP-B-SEARCH,ou=app",
	}}

	res := forward.Apply(userinfo, cfg)

	if got, want := res.Headers["X-Roles"], "APP-B-SEARCH"; got != want {
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
	if d.Reason != forward.ReasonNoMatches {
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

func TestApply_RealLDAPFixture_ProducesExpectedRolesAndIdentity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "userinfo_ldap.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var userinfo map[string]any
	if err := json.Unmarshal(raw, &userinfo); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	// Identity passthroughs are forwarded as HTTP headers but NOT published
	// to Mapped (no As set) — the SPA already has these fields in the raw
	// IdP response. Only "roles" opts into Mapped via As.
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "sub", Name: "X-User-Id"},
		{Claim: "email", Name: "X-User-Email"},
		{Claim: "cn", Name: "X-User-Name"},
		{Claim: "employeeNumber", Name: "X-User-EmployeeNumber"},
		{Claim: "privmaindepartmentcode", Name: "X-User-Department"},
		{
			Claim: "memberof",
			Name:  "X-User-Roles",
			As:    "roles",
			Mapping: []forward.Rule{
				{From: "cn=PT-XP-ROLE-APP-C-USER,", To: "APP-C-USER"},
				{From: "cn=PT-XP-ROLE-APP-B-SEARCH,", To: "APP-B-SEARCH"},
				{From: "cn=PT-XP-ROLE-APP-B-DOWNLOAD,", To: "APP-B-DOWNLOAD"},
				{From: "cn=GLOBAL-ROLE-GITHUB-CanJoin,", To: "GITHUB-MEMBER"},
			},
		},
	}}

	res := forward.Apply(userinfo, cfg)

	wantHeaders := map[string]string{
		"X-User-Id":             "12345678",
		"X-User-Email":          "user@example.com",
		"X-User-Name":           "TEST USER",
		"X-User-EmployeeNumber": "99999999",
		"X-User-Department":     "320",
		"X-User-Roles":          "APP-B-SEARCH,APP-B-DOWNLOAD,GITHUB-MEMBER,APP-C-USER",
	}
	if !reflect.DeepEqual(res.Headers, wantHeaders) {
		t.Errorf("headers mismatch:\ngot:  %v\nwant: %v", res.Headers, wantHeaders)
	}

	// Mapped should contain ONLY the entry that opted in via As.
	if len(res.Mapped) != 1 {
		t.Errorf("Mapped should contain only the As-opted entry, got %d entries: %v", len(res.Mapped), res.Mapped)
	}
	mappedRoles, ok := res.Mapped["roles"].([]string)
	if !ok {
		t.Fatalf("mapped[\"roles\"] not []string: %T", res.Mapped["roles"])
	}
	wantRoles := []string{"APP-B-SEARCH", "APP-B-DOWNLOAD", "GITHUB-MEMBER", "APP-C-USER"}
	if !reflect.DeepEqual(mappedRoles, wantRoles) {
		t.Errorf("mapped roles: got %v, want %v", mappedRoles, wantRoles)
	}
}
