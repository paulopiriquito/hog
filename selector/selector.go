// Package selector implements Kubernetes-style label selectors used to wire
// plugins and route-groups to routes.
package selector

import "slices"

// Requirement is a set-based selector term.
type Requirement struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"` // In, NotIn, Exists, DoesNotExist
	Values   []string `yaml:"values"`
}

// Selector matches a set of labels. The zero value (no terms) matches everything.
type Selector struct {
	MatchLabels      map[string]string `yaml:"matchLabels"`
	MatchExpressions []Requirement     `yaml:"matchExpressions"`
}

// Matches reports whether labels satisfy every term (logical AND).
func (s Selector) Matches(labels map[string]string) bool {
	for k, v := range s.MatchLabels {
		if labels[k] != v {
			return false
		}
	}
	for _, r := range s.MatchExpressions {
		val, present := labels[r.Key]
		switch r.Operator {
		case "In":
			if !present || !slices.Contains(r.Values, val) {
				return false
			}
		case "NotIn":
			if present && slices.Contains(r.Values, val) {
				return false
			}
		case "Exists":
			if !present {
				return false
			}
		case "DoesNotExist":
			if present {
				return false
			}
		default:
			return false // unknown operator never matches
		}
	}
	return true
}
