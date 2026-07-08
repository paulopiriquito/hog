package authz

import (
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/session"
)

// TestBuildInputNilPrincipalPresentButEmpty is the C1 regression: a nil
// principal must still populate subject/groups/claims (present, non-nil,
// empty) rather than omitting them. An omitted key makes a Rego reference
// like input.groups undefined, which can make a `not "admins" in
// input.groups` deny silently fail to fire — anonymous requests fail open.
func TestBuildInputNilPrincipalPresentButEmpty(t *testing.T) {
	r := httptest.NewRequest("DELETE", "http://h/admin", nil)
	in := buildInput(nil, r, "admin-route", nil)

	subject, ok := in["subject"]
	if !ok {
		t.Fatal("subject key missing for nil principal")
	}
	if subject != "" {
		t.Fatalf("subject = %v, want empty string", subject)
	}

	groupsRaw, ok := in["groups"]
	if !ok {
		t.Fatal("groups key missing for nil principal")
	}
	groups, ok := groupsRaw.([]string)
	if !ok || groups == nil {
		t.Fatalf("groups = %#v (%T), want non-nil []string", groupsRaw, groupsRaw)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %v, want empty", groups)
	}

	claimsRaw, ok := in["claims"]
	if !ok {
		t.Fatal("claims key missing for nil principal")
	}
	claims, ok := claimsRaw.(map[string]any)
	if !ok || claims == nil {
		t.Fatalf("claims = %#v (%T), want non-nil map[string]any", claimsRaw, claimsRaw)
	}
	if len(claims) != 0 {
		t.Fatalf("claims = %v, want empty", claims)
	}

	req, ok := in["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v, want map[string]any", in["request"])
	}
	if req["method"] != "DELETE" {
		t.Errorf("request.method = %v, want DELETE", req["method"])
	}
	if req["path"] != "/admin" {
		t.Errorf("request.path = %v, want /admin", req["path"])
	}
	if req["route_name"] != "admin-route" {
		t.Errorf("request.route_name = %v, want admin-route", req["route_name"])
	}
	if _, ok := req["route"]; !ok {
		t.Error("request.route missing")
	}
	labels, ok := req["labels"].(map[string]string)
	if !ok || labels == nil {
		t.Fatalf("request.labels = %#v, want non-nil map[string]string", req["labels"])
	}

	for _, key := range []string{"access_token", "accessToken", "AccessToken", "token"} {
		if _, present := in[key]; present {
			t.Fatalf("input must never carry %q", key)
		}
	}
}

// TestBuildInputPrincipalWithNilGroupsAndPassport covers a principal that
// exists but whose Groups/Passport are nil (e.g. a bare subject with no
// group/claim data yet resolved) — same present-but-empty guarantee as the
// nil-principal case.
func TestBuildInputPrincipalWithNilGroupsAndPassport(t *testing.T) {
	r := httptest.NewRequest("GET", "http://h/x", nil)
	p := &session.Principal{Subject: "u1", AccessToken: "super-secret-token"}
	in := buildInput(p, r, "r", nil)

	if in["subject"] != "u1" {
		t.Fatalf("subject = %v, want u1", in["subject"])
	}
	groups, ok := in["groups"].([]string)
	if !ok || groups == nil || len(groups) != 0 {
		t.Fatalf("groups = %#v, want non-nil empty []string", in["groups"])
	}
	claims, ok := in["claims"].(map[string]any)
	if !ok || claims == nil || len(claims) != 0 {
		t.Fatalf("claims = %#v, want non-nil empty map[string]any", in["claims"])
	}

	for _, key := range []string{"access_token", "accessToken", "AccessToken", "token"} {
		if _, present := in[key]; present {
			t.Fatalf("input must never carry %q", key)
		}
	}
}

// TestBuildInputReflectsPopulatedPrincipal is the positive case: a fully
// populated principal's subject/groups/claims flow through unchanged.
func TestBuildInputReflectsPopulatedPrincipal(t *testing.T) {
	r := httptest.NewRequest("GET", "http://h/x", nil)
	p := &session.Principal{
		Subject:  "alice",
		Groups:   []string{"admins", "ops"},
		Passport: map[string]any{"tier": "gold"},
	}
	in := buildInput(p, r, "r", map[string]string{"team": "platform"})

	if in["subject"] != "alice" {
		t.Fatalf("subject = %v, want alice", in["subject"])
	}
	groups, ok := in["groups"].([]string)
	if !ok || len(groups) != 2 || groups[0] != "admins" || groups[1] != "ops" {
		t.Fatalf("groups = %#v", in["groups"])
	}
	claims, ok := in["claims"].(map[string]any)
	if !ok || claims["tier"] != "gold" {
		t.Fatalf("claims = %#v", in["claims"])
	}
	req := in["request"].(map[string]any)
	labels := req["labels"].(map[string]string)
	if labels["team"] != "platform" {
		t.Fatalf("labels = %v", labels)
	}
}
