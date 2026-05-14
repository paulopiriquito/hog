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
//
// Mapping may be nil to mean "no rename rules, pass values through". An
// explicitly empty non-nil slice (e.g. []Rule{}) is treated as a config
// error and Validate rejects it. JSON/mapstructure unmarshalling produces
// nil for an absent field, which is the well-formed case.
//
// As is an optional SPA-facing alias. When set, Apply publishes the mapped
// value under this key in Result.Mapped (which the userinfo handler exposes
// to the SPA). When empty, the entry is forwarded as an HTTP header to
// upstream backends but is not published to the SPA. This lets operators
// pick a clean, JSON-friendly identifier for SPA consumption without
// coupling the SPA to HTTP-header naming conventions (e.g. the X- prefix).
type Header struct {
	Claim   string `mapstructure:"claim"             json:"claim"`
	Name    string `mapstructure:"header"            json:"header"`
	As      string `mapstructure:"as,omitempty"      json:"as,omitempty"`
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
	seenName := map[string]bool{}
	seenAs := map[string]bool{}
	for i, h := range c.Headers {
		if h.Claim == "" {
			return fmt.Errorf("forward.headers[%d]: claim is required", i)
		}
		if h.Name == "" {
			return fmt.Errorf("forward.headers[%d]: header is required", i)
		}
		if containsCRLF(h.Name) {
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
				if containsCRLF(r.To) {
					return fmt.Errorf("forward.headers[%d].mapping[%d]: to %q contains CR or LF", i, j, r.To)
				}
			}
		}
	}
	return nil
}

// containsCRLF reports whether s contains a carriage return or line feed,
// which would allow HTTP header injection if interpolated into a Set request.
// Operator-controlled fields are checked at config load time so misconfigs
// fail loud at startup rather than at request time with an opaque net/http
// "invalid header field value" error.
func containsCRLF(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}
