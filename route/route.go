// Package route defines Route and RouteGroup resources and resolves
// selector-based group policy onto routes.
package route

import (
	"fmt"

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
	Handler HandlerSpec `yaml:"handler"`
}

// Policy is the set of shared settings a RouteGroup applies to its routes.
// Extended by later specs; for now it carries the auth requirement only.
type Policy struct {
	Auth string `yaml:"auth"`
}

// RouteGroup applies a Policy to every route matching its selector.
type RouteGroup struct {
	Name     string
	Selector selector.Selector `yaml:"selector"`
	Policy   Policy            `yaml:"policy"`
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

// ResolvePolicy merges the policies of every group whose selector matches the
// given route labels. Later groups override earlier non-empty fields.
func ResolvePolicy(labels map[string]string, groups []RouteGroup) Policy {
	var p Policy
	for _, g := range groups {
		if g.Selector.Matches(labels) {
			if g.Policy.Auth != "" {
				p.Auth = g.Policy.Auth
			}
		}
	}
	return p
}
