// Package session provides HOG's encrypted, HttpOnly cookie session for the
// stateless BFF mode: a lean passport + mapped groups + access token, sealed
// with AES-256-GCM. The refresh token and id_token are never stored.
package session

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// GroupsConfig configures how the userinfo group-DN list is filtered and rendered.
type GroupsConfig struct {
	Source string   // userinfo claim holding the DN array (e.g. isMemberOf)
	Match  []string // keep DNs containing ANY of these (case-insensitive substring)
	Render string   // "cn" (extract cn= value) or "dn" (whole DN)
	As     string   // session field name for the rendered list (public view)
}

// Config is the validated session configuration.
type Config struct {
	CookieName         string
	Key                []byte        // exactly 32 bytes → AES-256-GCM
	TTL                time.Duration
	FingerprintHeaders []string
	PassportClaims     []string      // claims to persist (sub always kept; may be empty)
	Groups             *GroupsConfig // nil when unconfigured
	InfoPath           string
	PostLogoutRedirect string
}

// rawConfig mirrors the YAML; pointers distinguish "omitted" from "explicit empty".
type rawConfig struct {
	CookieName         string   `yaml:"cookieName"`
	Key                string   `yaml:"key"`
	TTL                string   `yaml:"ttl"`
	FingerprintHeaders []string `yaml:"fingerprintHeaders"`
	Passport           *struct {
		Claims *[]string `yaml:"claims"`
	} `yaml:"passport"`
	Groups *struct {
		Source string   `yaml:"source"`
		Match  []string `yaml:"match"`
		Render string   `yaml:"render"`
		As     string   `yaml:"as"`
	} `yaml:"groups"`
	InfoPath           string `yaml:"infoPath"`
	PostLogoutRedirect string `yaml:"postLogoutRedirect"`
}

var defaultPassportClaims = []string{"email", "name", "given_name", "family_name"}

// FromYAML parses and validates the Gateway `session` block.
func FromYAML(node yaml.Node) (Config, error) {
	var raw rawConfig
	if err := node.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("session: %w", err)
	}
	if len(raw.Key) != 32 {
		return Config{}, fmt.Errorf("session: key must be exactly 32 bytes (got %d)", len(raw.Key))
	}
	cfg := Config{
		CookieName:         orDefault(raw.CookieName, "hog_session"),
		Key:                []byte(raw.Key),
		TTL:                8 * time.Hour,
		FingerprintHeaders: raw.FingerprintHeaders,
		InfoPath:           orDefault(raw.InfoPath, "/auth/session"),
		PostLogoutRedirect: orDefault(raw.PostLogoutRedirect, "/"),
	}
	if raw.TTL != "" {
		d, err := time.ParseDuration(raw.TTL)
		if err != nil {
			return Config{}, fmt.Errorf("session: ttl: %w", err)
		}
		cfg.TTL = d
	}
	if len(cfg.FingerprintHeaders) == 0 {
		cfg.FingerprintHeaders = []string{"User-Agent"}
	}
	if raw.Passport == nil || raw.Passport.Claims == nil {
		cfg.PassportClaims = append([]string(nil), defaultPassportClaims...)
	} else {
		cfg.PassportClaims = *raw.Passport.Claims
	}
	if raw.Groups != nil {
		g := &GroupsConfig{
			Source: raw.Groups.Source,
			Match:  raw.Groups.Match,
			Render: orDefault(raw.Groups.Render, "cn"),
			As:     orDefault(raw.Groups.As, "groups"),
		}
		if g.Render != "cn" && g.Render != "dn" {
			return Config{}, fmt.Errorf("session: groups.render must be cn or dn (got %q)", g.Render)
		}
		cfg.Groups = g
	}
	return cfg, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
