package forward

import (
	"errors"
	"fmt"
	"strings"
)

// Config is the top-level forward-mapping configuration.
type Config struct {
	Headers []Header `mapstructure:"headers" json:"headers"`
}

// Header declares one userinfo-claim → HTTP-header mapping.
type Header struct {
	Claim   string `mapstructure:"claim"             json:"claim"`             // userinfo claim path (dotted)
	Name    string `mapstructure:"header"            json:"header"`            // output HTTP header
	As      string `mapstructure:"as,omitempty"      json:"as,omitempty"`      // optional SPA-facing alias; empty = not published to mapped
	Mapping []Rule `mapstructure:"mapping,omitempty" json:"mapping,omitempty"` // optional filter/rename rules; nil = passthrough
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
	seenName := map[string]bool{}
	seenAs := map[string]bool{}
	for i, h := range c.Headers {
		if h.Claim == "" {
			return fmt.Errorf("forward.headers[%d]: claim is required", i)
		}
		if h.Name == "" {
			return fmt.Errorf("forward.headers[%d]: header is required", i)
		}
		if strings.ContainsAny(h.Name, "\r\n") {
			return fmt.Errorf("forward.headers[%d]: header %q contains CR or LF", i, h.Name)
		}
		if seenName[h.Name] {
			return fmt.Errorf("forward.headers[%d]: duplicate header %q", i, h.Name)
		}
		seenName[h.Name] = true
		if h.As != "" {
			if seenAs[h.As] {
				return fmt.Errorf("forward.headers[%d]: duplicate as %q", i, h.As)
			}
			seenAs[h.As] = true
		}
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
				if strings.ContainsAny(r.To, "\r\n") {
					return fmt.Errorf("forward.headers[%d].mapping[%d]: to %q contains CR or LF", i, j, r.To)
				}
			}
		}
	}
	return nil
}
