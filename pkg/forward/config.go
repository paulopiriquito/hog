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
type Header struct {
	Claim   string `mapstructure:"claim" json:"claim"`
	Header  string `mapstructure:"header" json:"header"`
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
		if h.Header == "" {
			return fmt.Errorf("forward.headers[%d]: header is required", i)
		}
		if seen[h.Header] {
			return fmt.Errorf("forward.headers[%d]: duplicate header %q", i, h.Header)
		}
		seen[h.Header] = true
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
