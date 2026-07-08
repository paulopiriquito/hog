package route

import (
	"testing"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/selector"
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

func TestResolveTypeInferenceAndDefaults(t *testing.T) {
	groups := []RouteGroup{}
	// static handler ⇒ app ⇒ default auth public
	app := Route{Name: "spa", Handler: HandlerSpec{Type: "static"}}
	r, err := Resolve(app, groups)
	if err != nil || r.Type != "app" || r.Auth != "public" {
		t.Fatalf("app resolve = %+v err=%v", r, err)
	}
	// reverse-proxy handler ⇒ service ⇒ default auth required
	svc := Route{Name: "api", Handler: HandlerSpec{Type: "reverse-proxy"}}
	r, err = Resolve(svc, groups)
	if err != nil || r.Type != "service" || r.Auth != "required" {
		t.Fatalf("service resolve = %+v err=%v", r, err)
	}
}

func TestResolveExplicitAndOverrides(t *testing.T) {
	// group sets type service; route inline overrides auth to public
	groups := []RouteGroup{{
		Name: "g", Selector: selectorMatchAll(), Type: "service",
		Policy: Policy{Auth: "required"},
	}}
	rt := Route{Name: "r", Labels: map[string]string{"x": "y"}, Handler: HandlerSpec{Type: "reverse-proxy"},
		Policy: Policy{Auth: "public"}}
	r, err := Resolve(rt, groups)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "service" { // from group
		t.Errorf("type = %q", r.Type)
	}
	if r.Auth != "public" { // route inline overrides group
		t.Errorf("auth = %q", r.Auth)
	}
	// explicit route type wins over group + inference
	rt.Type = "app"
	r, _ = Resolve(rt, groups)
	if r.Type != "app" {
		t.Errorf("route type override = %q", r.Type)
	}
}

func TestResolveInvalidValues(t *testing.T) {
	if _, err := Resolve(Route{Name: "r", Type: "bogus", Handler: HandlerSpec{Type: "static"}}, nil); err == nil {
		t.Fatal("want error for invalid type")
	}
	if _, err := Resolve(Route{Name: "r", Handler: HandlerSpec{Type: "static"}, Policy: Policy{Auth: "maybe"}}, nil); err == nil {
		t.Fatal("want error for invalid auth")
	}
}

func TestResolveProjectionOverlay(t *testing.T) {
	gp := &ProjectionConfig{Session: &SessionProjection{Groups: &GroupsProjection{Header: "X-Group"}}}
	rp := &ProjectionConfig{Session: &SessionProjection{Groups: &GroupsProjection{Header: "X-Role"}}}
	groups := []RouteGroup{{Name: "g", Selector: selectorMatchAll(), Policy: Policy{Projection: gp}}}
	rt := Route{Name: "r", Labels: map[string]string{"x": "y"}, Handler: HandlerSpec{Type: "static"}, Policy: Policy{Projection: rp}}
	r, _ := Resolve(rt, groups)
	if r.Projection == nil || r.Projection.Session.Groups.Header != "X-Role" {
		t.Fatalf("route projection should win: %+v", r.Projection)
	}
}

func TestResolveLaterGroupWins(t *testing.T) {
	groups := []RouteGroup{
		{Name: "g1", Selector: selectorMatchAll(), Type: "app", Policy: Policy{Auth: "public"}},
		{Name: "g2", Selector: selectorMatchAll(), Type: "service", Policy: Policy{Auth: "required"}},
	}
	rt := Route{Name: "r", Labels: map[string]string{"x": "y"}, Handler: HandlerSpec{Type: "static"}}
	r, err := Resolve(rt, groups)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "service" || r.Auth != "required" {
		t.Fatalf("later group should win: type=%q auth=%q", r.Type, r.Auth)
	}
}

func TestResolveEmptyGroupDoesNotClobber(t *testing.T) {
	proj := &ProjectionConfig{Session: &SessionProjection{Groups: &GroupsProjection{Header: "X-Keep"}}}
	groups := []RouteGroup{
		{Name: "g1", Selector: selectorMatchAll(), Type: "service", Policy: Policy{Auth: "required", Projection: proj}},
		{Name: "g2", Selector: selectorMatchAll()}, // matches, but Type/Auth/Projection all empty/nil
	}
	rt := Route{Name: "r", Labels: map[string]string{"x": "y"}, Handler: HandlerSpec{Type: "reverse-proxy"}}
	r, err := Resolve(rt, groups)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "service" || r.Auth != "required" {
		t.Fatalf("empty later group must not clobber: type=%q auth=%q", r.Type, r.Auth)
	}
	if r.Projection == nil || r.Projection.Session.Groups.Header != "X-Keep" {
		t.Fatalf("empty later group must not clobber projection: %+v", r.Projection)
	}
}

func selectorMatchAll() selector.Selector { return selector.Selector{} }

func matchLabels(t *testing.T, k, v string) selector.Selector {
	t.Helper()
	return selector.Selector{MatchLabels: map[string]string{k: v}}
}

func TestResolveUnionsPolicies(t *testing.T) {
	rt := Route{
		Name: "r", Labels: map[string]string{"tier": "api"},
		Match: "/x", Handler: HandlerSpec{Type: "health"},
		Policies: []string{"p1", "p2"},
	}
	groups := []RouteGroup{
		{Name: "g1", Selector: matchLabels(t, "tier", "api"), Policies: []string{"p2", "p3"}}, // p2 dup
		{Name: "g2", Selector: matchLabels(t, "tier", "web"), Policies: []string{"p4"}},       // no match
	}
	res, err := Resolve(rt, groups)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range res.Policies {
		if got[p] {
			t.Fatalf("duplicate policy in resolved set: %q (%v)", p, res.Policies)
		}
		got[p] = true
	}
	for _, want := range []string{"p1", "p2", "p3"} {
		if !got[want] {
			t.Fatalf("missing policy %q in %v", want, res.Policies)
		}
	}
	if got["p4"] {
		t.Fatalf("non-matching group's policy leaked in: %v", res.Policies)
	}
}
