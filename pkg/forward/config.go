package forward

import (
	"errors"
	"fmt"
)

// Config is the top-level forward-mapping configuration.
type Config struct {
	Headers []Header `mapstructure:"headers" json:"headers"`
}

// Header declares one userinfo-claim → HTTP-header mapping.
//
// Mapping may be nil to mean "no rename rules, pass values through". An
// explicitly empty non-nil slice (e.g. []Rule{}) is treated as a config
// error and Validate rejects it. JSON/mapstructure unmarshalling produces
// nil for an absent field, which is the well-formed case.
type Header struct {
	Claim   string `mapstructure:"claim"             json:"claim"`
	Name    string `mapstructure:"header"            json:"header"`
	Mapping []Rule `mapstructure:"mapping,omitempty" json:"mapping,omitempty"`
}

// Rule is one substring filter/rename rule within a Header's Mapping.
type Rule struct {
	From string `mapstructure:"from" json:"from"`
	To   string `mapstructure:"to" json:"to"`
}

// Validate checks the Config for shape errors and returns the first one found.
func (c Config) Validate() error {
	if len(c.Headers) == 0 {
		return errors.New("forward: headers list is empty")
	}
	seen := map[string]bool{}
	for i, h := range c.Headers {
		if h.Claim == "" {
			return fmt.Errorf("forward.headers[%d]: claim is required", i)
		}
		if h.Name == "" {
			return fmt.Errorf("forward.headers[%d]: header is required", i)
		}
		if seen[h.Name] {
			return fmt.Errorf("forward.headers[%d]: duplicate header %q", i, h.Name)
		}
		seen[h.Name] = true
		if h.Mapping != nil {
			if len(h.Mapping) == 0 {
				return fmt.Errorf("forward.headers[%d]: mapping is present but empty", i)
			}
			for j, r := range h.Mapping {
				if r.From == "" {
					return fmt.Errorf("forward.headers[%d].mapping[%d]: from is required", i, j)
				}
				if r.To == "" {
					return fmt.Errorf("forward.headers[%d].mapping[%d]: to is required", i, j)
				}
			}
		}
	}
	return nil
}
