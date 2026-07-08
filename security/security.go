// Package security builds the security chain stage: CSRF protection
// (net/http.CrossOriginProtection) plus configurable security response headers.
// It is the only package importing http.CrossOriginProtection.
package security

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/paulopiriquito/hog/chain"
	"gopkg.in/yaml.v3"
)

// Config is the parsed spec.security block. A nil string/bool pointer means
// "unset ⇒ secure default"; an explicit empty string disables that header.
type Config struct {
	CSRF    CSRFConfig    `yaml:"csrf"`
	Headers HeadersConfig `yaml:"headers"`
}

// CSRFConfig configures net/http.CrossOriginProtection. CSRF protection is on
// by default; set Enabled to false to disable it entirely.
type CSRFConfig struct {
	Enabled        *bool    `yaml:"enabled"`
	TrustedOrigins []string `yaml:"trustedOrigins"`
	BypassPatterns []string `yaml:"bypassPatterns"`
}

// HeadersConfig configures the static security response headers.
type HeadersConfig struct {
	FrameOptions          *string    `yaml:"frameOptions"`
	ContentTypeOptions    *string    `yaml:"contentTypeOptions"`
	ReferrerPolicy        *string    `yaml:"referrerPolicy"`
	HSTS                  HSTSConfig `yaml:"hsts"`
	ContentSecurityPolicy string     `yaml:"contentSecurityPolicy"`
}

// HSTSConfig configures the Strict-Transport-Security header. HSTS is on by
// default; set Enabled to false to disable it.
type HSTSConfig struct {
	Enabled           *bool `yaml:"enabled"`
	MaxAge            *int  `yaml:"maxAge"`
	IncludeSubDomains *bool `yaml:"includeSubDomains"`
	Preload           bool  `yaml:"preload"`
}

// Parse decodes a spec.security node (a zero node ⇒ all defaults).
func Parse(n yaml.Node) (Config, error) {
	var c Config
	if n.Kind == 0 {
		return c, nil
	}
	if err := n.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("security: %w", err)
	}
	return c, nil
}

func deref(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Build composes the CSRF + security-headers middleware from cfg.
func Build(cfg Config) (chain.Middleware, error) {
	headers := headersHandler(cfg.Headers)

	if !derefBool(cfg.CSRF.Enabled, true) {
		return chain.Func(headers), nil
	}
	cop := http.NewCrossOriginProtection()
	for _, o := range cfg.CSRF.TrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("security: trustedOrigin %q: %w", o, err)
		}
	}
	for _, p := range cfg.CSRF.BypassPatterns {
		if err := addBypass(cop, p); err != nil {
			return nil, err
		}
	}
	// headers outermost (set on every response, including CSRF 403s), CSRF inner.
	return chain.Func(func(next http.Handler) http.Handler {
		return headers(cop.Handler(next))
	}), nil
}

// addBypass adds an insecure bypass pattern, converting the stdlib's panic on a
// malformed/conflicting pattern into a fail-fast config error.
func addBypass(cop *http.CrossOriginProtection, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("security: invalid bypassPattern %q: %v", pattern, r)
		}
	}()
	cop.AddInsecureBypassPattern(pattern)
	return nil
}

func headersHandler(h HeadersConfig) func(http.Handler) http.Handler {
	frame := deref(h.FrameOptions, "DENY")
	nosniff := deref(h.ContentTypeOptions, "nosniff")
	referrer := deref(h.ReferrerPolicy, "strict-origin-when-cross-origin")
	hstsOn := derefBool(h.HSTS.Enabled, true)
	hsts := hstsValue(h.HSTS)
	csp := h.ContentSecurityPolicy
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hd := w.Header()
			if frame != "" {
				hd.Set("X-Frame-Options", frame)
			}
			if nosniff != "" {
				hd.Set("X-Content-Type-Options", nosniff)
			}
			if referrer != "" {
				hd.Set("Referrer-Policy", referrer)
			}
			if hstsOn {
				hd.Set("Strict-Transport-Security", hsts)
			}
			if csp != "" {
				hd.Set("Content-Security-Policy", csp)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hstsValue(h HSTSConfig) string {
	maxAge := 31536000
	if h.MaxAge != nil {
		maxAge = *h.MaxAge
	}
	parts := []string{"max-age=" + strconv.Itoa(maxAge)}
	if derefBool(h.IncludeSubDomains, true) {
		parts = append(parts, "includeSubDomains")
	}
	if h.Preload {
		parts = append(parts, "preload")
	}
	return strings.Join(parts, "; ")
}
