package idp

import (
	"context"
	"fmt"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// Register adds the built-in `oidc` IdP to reg.
func Register(reg *registry.Registry) {
	reg.Register(config.KindIdP, "oidc", func(name string, cfg registry.RawConfig) (any, error) {
		var oc oidcConfig
		if err := cfg.Decode(&oc); err != nil {
			return nil, fmt.Errorf("idp %q: %w", name, err)
		}
		p, err := newOIDC(context.Background(), oc)
		if err != nil {
			return nil, fmt.Errorf("idp %q: %w", name, err)
		}
		return p, nil
	})
}
