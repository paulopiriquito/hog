package auth

import (
	"fmt"

	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

// Config is the parsed gateway `auth` block.
type Config struct {
	LoginPath  string
	LogoutPath string
}

type rawConfig struct {
	LoginPath  string `yaml:"loginPath"`
	LogoutPath string `yaml:"logoutPath"`
}

// FromYAML parses the auth block (a zero node ⇒ all defaults).
func FromYAML(node yaml.Node) (Config, error) {
	var raw rawConfig
	if node.Kind != 0 {
		if err := node.Decode(&raw); err != nil {
			return Config{}, fmt.Errorf("auth: %w", err)
		}
	}
	c := Config{
		LoginPath:  orDefault(raw.LoginPath, "/auth/login"),
		LogoutPath: orDefault(raw.LogoutPath, "/auth/logout"),
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// idTokenDefaultClaims are present in the id_token under profile+email scopes,
// so they need no userinfo fetch.
var idTokenDefaultClaims = map[string]bool{
	"sub": true, "email": true, "name": true, "given_name": true, "family_name": true,
}

// NeedsUserInfo decides whether the cookie callback must call the userinfo
// endpoint, from the identity model (mode + configured claims/groups).
func NeedsUserInfo(idCfg session.IdentityConfig) bool {
	switch idCfg.UserInfo {
	case "always":
		return true
	case "never":
		return false
	default: // auto
		if idCfg.Groups != nil {
			return true
		}
		for _, c := range idCfg.Claims {
			if !idTokenDefaultClaims[c] {
				return true
			}
		}
		return false
	}
}
