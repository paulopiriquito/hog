// Package gateway parses the Gateway root resource into runtime settings.
package gateway

import (
	"fmt"

	"github.com/paulopiriquito/hog/config"
	"gopkg.in/yaml.v3"
)

// Settings is the gateway-level runtime configuration. Plugins is the
// build-time module manifest; it is parsed here but consumed by the build
// tool, not at runtime.
type Settings struct {
	Listen         string    `yaml:"listen"`
	TrustedProxies []string  `yaml:"trustedProxies"`
	Plugins        []string  `yaml:"plugins"`
	Session        yaml.Node `yaml:"session"`       // raw; parsed by the session package
	Identity       yaml.Node `yaml:"identity"`      // raw; parsed by the session package (ParseIdentity)
	Auth           yaml.Node `yaml:"auth"`          // raw; parsed by the auth package
	StateProvider  yaml.Node `yaml:"stateProvider"` // raw; parsed by the session package (ParseStateProvider)
}

// FromResource decodes a Gateway resource's spec, applying defaults.
func FromResource(r config.Resource) (Settings, error) {
	if r.Kind != config.KindGateway {
		return Settings{}, fmt.Errorf("gateway: expected kind Gateway, got %q", r.Kind)
	}
	var s Settings
	if err := r.Spec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("gateway %q: %w", r.Metadata.Name, err)
	}
	if s.Listen == "" {
		s.Listen = ":8080"
	}
	return s, nil
}
