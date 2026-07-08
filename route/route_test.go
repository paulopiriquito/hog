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

func TestParseRouteCapturesHandlerConfig(t *testing.T) {
	rs, err := config.DecodeAll([]byte(`
kind: Route
metadata: { name: spa }
spec:
  match: /
  handler: { type: static, dir: /web, index: app.html }
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRoute(rs[0])
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if r.Handler.Type != "static" {
		t.Fatalf("Handler.Type = %q, want static", r.Handler.Type)
	}
	var hc struct {
		Dir   string `yaml:"dir"`
		Index string `yaml:"index"`
	}
	if err := r.Handler.Config.Decode(&hc); err != nil {
		t.Fatalf("decode Handler.Config: %v", err)
	}
	if hc.Dir != "/web" || hc.Index != "app.html" {
		t.Fatalf("Handler.Config decoded = %+v, want dir=/web index=app.html", hc)
	}
}
