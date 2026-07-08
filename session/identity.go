package session

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// IdentityConfig is the shared identity model (cookie sessions AND bearer): the
// passport claim allowlist, optional group mapping, and the userinfo-fetch mode.
type IdentityConfig struct {
	Claims   []string      // passport allowlist (sub always kept separately)
	Groups   *GroupsConfig // nil when unconfigured
	UserInfo string        // auto | always | never
}

type rawIdentity struct {
	Claims *[]string `yaml:"claims"`
	Groups *struct {
		Source string   `yaml:"source"`
		Match  []string `yaml:"match"`
		Render string   `yaml:"render"`
		As     string   `yaml:"as"`
	} `yaml:"groups"`
	UserInfo string `yaml:"userInfo"`
}

// ParseIdentity parses the Gateway `identity:` block. A zero node ⇒ all defaults
// (default passport claims, no groups, userInfo auto).
func ParseIdentity(node yaml.Node) (IdentityConfig, error) {
	var raw rawIdentity
	if node.Kind != 0 {
		if err := node.Decode(&raw); err != nil {
			return IdentityConfig{}, fmt.Errorf("identity: %w", err)
		}
	}
	cfg := IdentityConfig{UserInfo: orDefault(raw.UserInfo, "auto")}
	switch cfg.UserInfo {
	case "auto", "always", "never":
	default:
		return IdentityConfig{}, fmt.Errorf("identity: userInfo must be auto|always|never (got %q)", cfg.UserInfo)
	}
	if raw.Claims == nil {
		cfg.Claims = append([]string(nil), defaultPassportClaims...)
	} else {
		cfg.Claims = *raw.Claims
	}
	if raw.Groups != nil {
		g := &GroupsConfig{
			Source: raw.Groups.Source,
			Match:  raw.Groups.Match,
			Render: orDefault(raw.Groups.Render, "cn"),
			As:     orDefault(raw.Groups.As, "groups"),
		}
		if g.Render != "cn" && g.Render != "dn" {
			return IdentityConfig{}, fmt.Errorf("identity: groups.render must be cn or dn (got %q)", g.Render)
		}
		cfg.Groups = g
	}
	return cfg, nil
}

// NewPrincipal builds a request-context Principal from verified token claims, an
// optional userinfo map, and the access token, using the same passport/group
// projection as the cookie session — so a Bearer principal projects identically.
func NewPrincipal(sub string, idClaims, userinfo map[string]any, accessToken string, idCfg IdentityConfig) *Principal {
	return &Principal{
		Subject:     sub,
		Passport:    projectPassport(idCfg.Claims, idClaims, userinfo),
		Groups:      projectGroups(idCfg.Groups, userinfo, idClaims),
		AccessToken: accessToken,
	}
}

// NeedUserInfoForToken decides whether a verified Bearer token still needs a
// userinfo fetch: "token claims first" — fetch only when the token is missing a
// configured passport claim or the group source.
func NeedUserInfoForToken(idCfg IdentityConfig, tokenClaims map[string]any) bool {
	switch idCfg.UserInfo {
	case "always":
		return true
	case "never":
		return false
	default: // auto
		if idCfg.Groups != nil {
			if _, ok := tokenClaims[idCfg.Groups.Source]; !ok {
				return true
			}
		}
		for _, c := range idCfg.Claims {
			if _, ok := tokenClaims[c]; !ok {
				return true
			}
		}
		return false
	}
}
