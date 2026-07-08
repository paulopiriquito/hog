package authz

import (
	"testing"

	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

func TestStringOrListDecode(t *testing.T) {
	var s struct {
		A StringOrList `yaml:"a"`
		B StringOrList `yaml:"b"`
	}
	if err := yaml.Unmarshal([]byte("a: one\nb: [x, y]\n"), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.A) != 1 || s.A[0] != "one" {
		t.Fatalf("scalar decode = %v", s.A)
	}
	if len(s.B) != 2 || s.B[1] != "y" {
		t.Fatalf("seq decode = %v", s.B)
	}
}

func TestSatisfied(t *testing.T) {
	admin := &session.Principal{Subject: "u", Groups: []string{"admins"},
		Passport: map[string]any{"department": "engineering", "tier": "gold"}}

	cases := []struct {
		name string
		req  Require
		p    *session.Principal
		want bool
	}{
		{"empty require allows", Require{}, admin, true},
		{"group present", Require{Groups: []string{"admins", "ops"}}, admin, true},
		{"group absent", Require{Groups: []string{"ops"}}, admin, false},
		{"claim scalar match", Require{Claims: map[string]StringOrList{"department": {"engineering"}}}, admin, true},
		{"claim list any-of", Require{Claims: map[string]StringOrList{"tier": {"gold", "platinum"}}}, admin, true},
		{"claim mismatch", Require{Claims: map[string]StringOrList{"department": {"sales"}}}, admin, false},
		{"claim missing", Require{Claims: map[string]StringOrList{"nope": {"x"}}}, admin, false},
		{"group AND claim", Require{Groups: []string{"admins"}, Claims: map[string]StringOrList{"tier": {"gold"}}}, admin, true},
		{"nil principal with constraint denies", Require{Groups: []string{"admins"}}, nil, false},
		{"nil principal empty require allows", Require{}, nil, true},
	}
	for _, c := range cases {
		if got := Satisfied(c.req, c.p); got != c.want {
			t.Errorf("%s: Satisfied = %v, want %v", c.name, got, c.want)
		}
	}
}
