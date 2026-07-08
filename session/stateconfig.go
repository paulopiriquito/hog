package session

import (
	"context"
	"fmt"
	"time"

	"github.com/paulopiriquito/hog/idp"
	"gopkg.in/yaml.v3"
)

// Refresher is the narrow capability the stateful manager needs from the IdP:
// exchange a refresh token for fresh tokens. idp.IdP satisfies it.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*idp.Tokens, error)
}

// StateProviderConfig is the parsed gateway `stateProvider` block. Config is the
// opaque sub-block passed verbatim to the registered StateProvider plugin factory.
type StateProviderConfig struct {
	Type        string
	RefreshSkew time.Duration // how early before the access token's expiry to silently refresh
	KeyPrefix   string
	Config      yaml.Node // opaque; passed verbatim to the registered StateProvider plugin factory
}

type rawStateProvider struct {
	Type        string    `yaml:"type"`
	RefreshSkew string    `yaml:"refreshSkew"`
	KeyPrefix   string    `yaml:"keyPrefix"`
	Config      yaml.Node `yaml:"config"`
}

// ParseStateProvider parses the `stateProvider` block. `type` is required;
// refreshSkew defaults to 60s, keyPrefix to "hog:sess:".
func ParseStateProvider(node yaml.Node) (StateProviderConfig, error) {
	var raw rawStateProvider
	if err := node.Decode(&raw); err != nil {
		return StateProviderConfig{}, fmt.Errorf("stateProvider: %w", err)
	}
	if raw.Type == "" {
		return StateProviderConfig{}, fmt.Errorf("stateProvider: type is required")
	}
	skew := 60 * time.Second
	if raw.RefreshSkew != "" {
		d, err := time.ParseDuration(raw.RefreshSkew)
		if err != nil {
			return StateProviderConfig{}, fmt.Errorf("stateProvider: refreshSkew: %w", err)
		}
		if d < 0 {
			return StateProviderConfig{}, fmt.Errorf("stateProvider: refreshSkew must not be negative (got %s)", d)
		}
		skew = d
	}
	prefix := raw.KeyPrefix
	if prefix == "" {
		prefix = "hog:sess:"
	}
	return StateProviderConfig{Type: raw.Type, RefreshSkew: skew, KeyPrefix: prefix, Config: raw.Config}, nil
}
