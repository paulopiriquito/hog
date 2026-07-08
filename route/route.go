// Package route defines Route and RouteGroup resources and resolves each
// route's effective access block from selector-matched groups.
package route

import (
	"fmt"
	"strings"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/selector"
	"gopkg.in/yaml.v3"
)

// HandlerSpec selects a terminal handler module and carries its config. Config
// holds the entire handler mapping node — INCLUDING the `type` key — so the
// terminal factory can decode handler-specific fields (e.g. dir, cacheControl)
// from it. Factory config structs must therefore tolerate the extra `type` key
// (do not enable yaml KnownFields(true) when decoding it).
type HandlerSpec struct {
	Type   string
	Config yaml.Node
}

// UnmarshalYAML captures the handler's type and stores the full mapping node
// as Config so terminal factories can decode their own fields from it.
// yaml.Node with ",inline" does not capture remainder fields in gopkg.in/yaml.v3;
// a custom unmarshaler is required.
func (h *HandlerSpec) UnmarshalYAML(node *yaml.Node) error {
	type plain struct {
		Type string `yaml:"type"`
	}
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	h.Type = p.Type
	h.Config = *node
	return nil
}

// Route is a single routable endpoint.
type Route struct {
	Name    string
	Labels  map[string]string
	Match   string      `yaml:"match"`
	Type    string      `yaml:"type"`
	Handler HandlerSpec `yaml:"handler"`
	Access  AccessSpec  `yaml:"access"`
}

// AccessSpec is a route's (or group's) access-control block: authentication,
// authorization policy references, and identity projection.
type AccessSpec struct {
	Auth       string            `yaml:"auth"`      // required | public
	Authorize  []string          `yaml:"authorize"` // names of kind: Policy resources
	Projection *ProjectionConfig `yaml:"projection"`
}

// ProjectionConfig customizes identity-header projection. The request section is
// reserved (request-header forwarding is a deferred follow-up).
type ProjectionConfig struct {
	Session *SessionProjection `yaml:"session"`
	Request yaml.Node          `yaml:"request"` // reserved; no effect in #3b
}

// SessionProjection overrides the derive-from-passport defaults.
type SessionProjection struct {
	Claims map[string]string `yaml:"claims"` // claim name → header name (explicit set)
	Groups *GroupsProjection `yaml:"groups"`
}

// GroupsProjection overrides the groups header name (default derives from session Groups.As).
type GroupsProjection struct {
	Header string `yaml:"header"`
}

// RouteGroup applies an access block to every route matching its selector.
type RouteGroup struct {
	Name     string
	Type     string            `yaml:"type"`
	Selector selector.Selector `yaml:"selector"`
	Access   AccessSpec        `yaml:"access"`
}

// ParseRoute decodes a Route resource.
func ParseRoute(r config.Resource) (Route, error) {
	if r.Kind != config.KindRoute {
		return Route{}, fmt.Errorf("route: expected kind Route, got %q", r.Kind)
	}
	out := Route{Name: r.Metadata.Name, Labels: r.Metadata.Labels}
	if err := r.Spec.Decode(&out); err != nil {
		return Route{}, fmt.Errorf("route %q: %w", r.Metadata.Name, err)
	}
	if out.Match == "" {
		return Route{}, fmt.Errorf("route %q: spec.match is required", r.Metadata.Name)
	}
	if out.Handler.Type == "" {
		return Route{}, fmt.Errorf("route %q: spec.handler.type is required", r.Metadata.Name)
	}
	return out, nil
}

// ParseGroup decodes a RouteGroup resource.
func ParseGroup(r config.Resource) (RouteGroup, error) {
	if r.Kind != config.KindRouteGroup {
		return RouteGroup{}, fmt.Errorf("route: expected kind RouteGroup, got %q", r.Kind)
	}
	out := RouteGroup{Name: r.Metadata.Name}
	if err := r.Spec.Decode(&out); err != nil {
		return RouteGroup{}, fmt.Errorf("routegroup %q: %w", r.Metadata.Name, err)
	}
	return out, nil
}

// Resolved is a route's effective type, auth, projection, and authorization set.
type Resolved struct {
	Type       string // app | service
	Auth       string // required | public
	Projection *ProjectionConfig
	Authorize  []string // names of kind: Policy resources to enforce
}

// Resolve computes a route's effective type, auth, projection, and authorization
// set (the route's own access.authorize unioned with every matching RouteGroup's,
// deduped) from the route's own fields, matching RouteGroups (document order,
// later wins for scalars), and type-inferred defaults.
func Resolve(rt Route, groups []RouteGroup) (Resolved, error) {
	var gType, gAuth string
	var proj *ProjectionConfig
	for _, g := range groups {
		if g.Selector.Matches(rt.Labels) {
			if g.Type != "" {
				gType = g.Type
			}
			if g.Access.Auth != "" {
				gAuth = g.Access.Auth
			}
			if g.Access.Projection != nil {
				proj = g.Access.Projection
			}
		}
	}

	typ := firstNonEmpty(rt.Type, gType)
	if typ == "" {
		typ = inferType(rt.Handler.Type)
	}
	typ = strings.ToLower(typ)
	if typ != "app" && typ != "service" {
		return Resolved{}, fmt.Errorf("route %q: invalid type %q (want app|service)", rt.Name, typ)
	}

	auth := firstNonEmpty(rt.Access.Auth, gAuth)
	if auth == "" {
		if typ == "service" {
			auth = "required"
		} else {
			auth = "public"
		}
	}
	auth = strings.ToLower(auth)
	if auth != "required" && auth != "public" {
		return Resolved{}, fmt.Errorf("route %q: invalid auth %q (want required|public)", rt.Name, auth)
	}

	if rt.Access.Projection != nil {
		proj = rt.Access.Projection
	}

	var authorize []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if n != "" && !seen[n] {
				seen[n] = true
				authorize = append(authorize, n)
			}
		}
	}
	add(rt.Access.Authorize)
	for _, g := range groups {
		if g.Selector.Matches(rt.Labels) {
			add(g.Access.Authorize)
		}
	}

	return Resolved{Type: typ, Auth: auth, Projection: proj, Authorize: authorize}, nil
}

// inferType maps a handler type to a default route type.
func inferType(handlerType string) string {
	switch handlerType {
	case "reverse-proxy", "api":
		return "service"
	default: // static, health, system, …
		return "app"
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
