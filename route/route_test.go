package route

import (
	"testing"

	"github.com/paulopiriquito/hog/config"
)

func TestParseRoute(t *testing.T) {
	rs, _ := config.DecodeAll([]byte(`
kind: Route
metadata: { name: dashboard, labels: { app: dash, tier: api } }
spec:
  match: /api/dashboard
  handler: { type: health }
`))
	r, err := ParseRoute(rs[0])
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if r.Match != "/api/dashboard" || r.Handler.Type != "health" {
		t.Fatalf("route = %+v", r)
	}
	if r.Labels["tier"] != "api" {
		t.Fatalf("labels = %v", r.Labels)
	}
}

func TestResolvePolicyFromGroups(t *testing.T) {
	rs, _ := config.DecodeAll([]byte(`
kind: RouteGroup
metadata: { name: app-auth }
spec:
  selector: { matchLabels: { app: dash } }
  policy: { auth: required }
`))
	g, err := ParseGroup(rs[0])
	if err != nil {
		t.Fatalf("ParseGroup: %v", err)
	}
	// Matching route inherits the policy; non-matching does not.
	matched := ResolvePolicy(map[string]string{"app": "dash"}, []RouteGroup{g})
	if matched.Auth != "required" {
		t.Fatalf("matched policy = %+v, want auth=required", matched)
	}
	unmatched := ResolvePolicy(map[string]string{"app": "other"}, []RouteGroup{g})
	if unmatched.Auth != "" {
		t.Fatalf("unmatched policy = %+v, want empty", unmatched)
	}
}
