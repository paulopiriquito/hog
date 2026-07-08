package selector

import "testing"

func TestMatches(t *testing.T) {
	labels := map[string]string{"app": "dash", "tier": "api"}
	cases := []struct {
		name string
		sel  Selector
		want bool
	}{
		{name: "empty matches all", sel: Selector{}, want: true},
		{name: "matchLabels hit", sel: Selector{MatchLabels: map[string]string{"app": "dash"}}, want: true},
		{name: "matchLabels miss value", sel: Selector{MatchLabels: map[string]string{"app": "other"}}, want: false},
		{name: "matchLabels miss key", sel: Selector{MatchLabels: map[string]string{"nope": "x"}}, want: false},
		{name: "In hit", sel: Selector{MatchExpressions: []Requirement{{Key: "tier", Operator: "In", Values: []string{"api", "web"}}}}, want: true},
		{name: "In miss", sel: Selector{MatchExpressions: []Requirement{{Key: "tier", Operator: "In", Values: []string{"web"}}}}, want: false},
		{name: "NotIn hit", sel: Selector{MatchExpressions: []Requirement{{Key: "tier", Operator: "NotIn", Values: []string{"web"}}}}, want: true},
		{name: "Exists hit", sel: Selector{MatchExpressions: []Requirement{{Key: "app", Operator: "Exists"}}}, want: true},
		{name: "DoesNotExist hit", sel: Selector{MatchExpressions: []Requirement{{Key: "zone", Operator: "DoesNotExist"}}}, want: true},
		{name: "AND of label+expr", sel: Selector{MatchLabels: map[string]string{"app": "dash"}, MatchExpressions: []Requirement{{Key: "tier", Operator: "In", Values: []string{"web"}}}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sel.Matches(labels); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}
