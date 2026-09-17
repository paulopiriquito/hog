package session

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// IdentityConfig is the shared identity model (cookie sessions AND bearer): the
// passport claim allowlist, optional group mapping, and the userinfo-fetch mode.
type IdentityConfig struct {
	Claims       []string      // passport allowlist (sub always kept separately)
	Groups       *GroupsConfig // nil when unconfigured
	UserInfo     string        // auto | always | never
	SubjectClaim string        // claim that becomes Principal.Subject; "sub" = the token's own subject
	Assertion    *AssertionConfig
}

// AssertionConfig configures the identity hand-off between HOG instances: the
// upstream instance issues a short-lived signed assertion of the principal it
// resolved; the downstream instance accepts it instead of resolving the identity
// against the IdP a second time.
type AssertionConfig struct {
	Issue  *AssertionIssueConfig
	Accept *AssertionAcceptConfig
}

// AssertionIssueConfig is the signing side.
type AssertionIssueConfig struct {
	Name   string        // iss claim, e.g. go-app
	KeyID  string        // kid header
	Seed   []byte        // 32-byte Ed25519 seed
	TTL    time.Duration // default 60s
	Header string        // default X-Hog-Identity
}

// AssertionAcceptConfig is the verifying side.
type AssertionAcceptConfig struct {
	Issuer        string            // expected iss
	Header        string            // default X-Hog-Identity
	Keys          map[string][]byte // kid -> 32-byte Ed25519 public key
	RequireBearer bool              // default true: the assertion only enriches a token-authenticated principal
}

type rawIdentity struct {
	Claims *[]string `yaml:"claims"`
	Groups *struct {
		Source string   `yaml:"source"`
		Match  []string `yaml:"match"`
		Render string   `yaml:"render"`
		As     string   `yaml:"as"`
		Strip  []string `yaml:"strip"`
	} `yaml:"groups"`
	UserInfo     string `yaml:"userInfo"`
	SubjectClaim string `yaml:"subjectClaim"`
	Assertion    *struct {
		Issue *struct {
			Name   string `yaml:"name"`
			KeyID  string `yaml:"keyId"`
			Key    string `yaml:"key"`
			TTL    string `yaml:"ttl"`
			Header string `yaml:"header"`
		} `yaml:"issue"`
		Accept *struct {
			Issuer        string `yaml:"issuer"`
			Header        string `yaml:"header"`
			RequireBearer *bool  `yaml:"requireBearer"`
			Keys          []struct {
				KeyID     string `yaml:"keyId"`
				PublicKey string `yaml:"publicKey"`
			} `yaml:"keys"`
		} `yaml:"accept"`
	} `yaml:"assertion"`
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
	cfg.SubjectClaim = orDefault(raw.SubjectClaim, "sub")
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
			Strip:  raw.Groups.Strip,
		}
		if g.Render != "cn" && g.Render != "dn" {
			return IdentityConfig{}, fmt.Errorf("identity: groups.render must be cn or dn (got %q)", g.Render)
		}
		cfg.Groups = g
	}
	if raw.Assertion != nil {
		a, err := parseAssertion(raw.Assertion)
		if err != nil {
			return IdentityConfig{}, err
		}
		cfg.Assertion = a
	}
	return cfg, nil
}

// parseAssertion validates the `identity.assertion` block: issue, accept, or
// both must be present — an empty block would silently do nothing.
func parseAssertion(raw *struct {
	Issue *struct {
		Name   string `yaml:"name"`
		KeyID  string `yaml:"keyId"`
		Key    string `yaml:"key"`
		TTL    string `yaml:"ttl"`
		Header string `yaml:"header"`
	} `yaml:"issue"`
	Accept *struct {
		Issuer        string `yaml:"issuer"`
		Header        string `yaml:"header"`
		RequireBearer *bool  `yaml:"requireBearer"`
		Keys          []struct {
			KeyID     string `yaml:"keyId"`
			PublicKey string `yaml:"publicKey"`
		} `yaml:"keys"`
	} `yaml:"accept"`
}) (*AssertionConfig, error) {
	if raw.Issue == nil && raw.Accept == nil {
		return nil, fmt.Errorf("identity.assertion: must configure issue, accept, or both")
	}
	cfg := &AssertionConfig{}
	if raw.Issue != nil {
		iss := raw.Issue
		if iss.Name == "" {
			return nil, fmt.Errorf("identity.assertion.issue: name is required")
		}
		if iss.KeyID == "" {
			return nil, fmt.Errorf("identity.assertion.issue: keyId is required")
		}
		if iss.Key == "" {
			return nil, fmt.Errorf("identity.assertion.issue: key is required")
		}
		seed, err := decodeKey(iss.Key, ed25519.SeedSize)
		if err != nil {
			return nil, fmt.Errorf("identity.assertion.issue: key: %w", err)
		}
		ttl := 60 * time.Second
		if iss.TTL != "" {
			d, err := time.ParseDuration(iss.TTL)
			if err != nil {
				return nil, fmt.Errorf("identity.assertion.issue: ttl: %w", err)
			}
			if d <= 0 {
				return nil, fmt.Errorf("identity.assertion.issue: ttl must be > 0 (got %s)", iss.TTL)
			}
			ttl = d
		}
		cfg.Issue = &AssertionIssueConfig{
			Name:   iss.Name,
			KeyID:  iss.KeyID,
			Seed:   seed,
			TTL:    ttl,
			Header: orDefault(iss.Header, "X-Hog-Identity"),
		}
	}
	if raw.Accept != nil {
		acc := raw.Accept
		if acc.Issuer == "" {
			return nil, fmt.Errorf("identity.assertion.accept: issuer is required")
		}
		if len(acc.Keys) == 0 {
			return nil, fmt.Errorf("identity.assertion.accept: at least one key is required")
		}
		keys := make(map[string][]byte, len(acc.Keys))
		for _, k := range acc.Keys {
			if k.KeyID == "" {
				return nil, fmt.Errorf("identity.assertion.accept: keys: keyId is required")
			}
			// Case-sensitive by design (correct for JWS kid matching), but an
			// operator footgun: "K1" and "k1" pass this check as distinct keys.
			if _, dup := keys[k.KeyID]; dup {
				return nil, fmt.Errorf("identity.assertion.accept: keys: duplicate keyId %q", k.KeyID)
			}
			pub, err := decodeKey(k.PublicKey, ed25519.PublicKeySize)
			if err != nil {
				return nil, fmt.Errorf("identity.assertion.accept: keys[%s]: publicKey: %w", k.KeyID, err)
			}
			keys[k.KeyID] = pub
		}
		requireBearer := true
		if acc.RequireBearer != nil {
			requireBearer = *acc.RequireBearer
		}
		cfg.Accept = &AssertionAcceptConfig{
			Issuer:        acc.Issuer,
			Header:        orDefault(acc.Header, "X-Hog-Identity"),
			Keys:          keys,
			RequireBearer: requireBearer,
		}
	}
	return cfg, nil
}

// decodeKey decodes base64-encoded Ed25519 key material, accepting both
// standard and raw-URL-safe base64: operators paste either form depending on
// which tool generated the key, and rejecting the "wrong" one would just be a
// confusing surprise. It also enforces the expected decoded length.
func decodeKey(s string, wantLen int) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("invalid base64: %w", err)
		}
	}
	if len(b) != wantLen {
		return nil, fmt.Errorf("must decode to %d bytes (got %d)", wantLen, len(b))
	}
	return b, nil
}

// NewPrincipal builds a request-context Principal from verified token claims, an
// optional userinfo map, and the access token, using the same passport/group
// projection as the cookie session — so a Bearer principal projects identically.
func NewPrincipal(sub string, idClaims, userinfo map[string]any, accessToken string, idCfg IdentityConfig) *Principal {
	return &Principal{
		Subject:     resolveSubject(idCfg.SubjectClaim, sub, idClaims, userinfo),
		Passport:    projectPassport(idCfg.Claims, idClaims, userinfo),
		Groups:      projectGroups(idCfg.Groups, userinfo, idClaims),
		AccessToken: accessToken,
	}
}

// NeedUserInfoForToken decides whether a verified Bearer token still needs a
// userinfo fetch: "token claims first" — fetch only when the token is missing a
// configured passport claim, the group source, or a usable value for the
// configured subject claim.
func NeedUserInfoForToken(idCfg IdentityConfig, tokenClaims map[string]any) bool {
	switch idCfg.UserInfo {
	case "always":
		return true
	case "never":
		return false
	default: // auto
		if idCfg.SubjectClaim != "" && idCfg.SubjectClaim != "sub" {
			// A present-but-unusable claim (empty or not a string) is as good as absent:
			// userinfo is the only remaining place the subject can come from.
			if v, ok := tokenClaims[idCfg.SubjectClaim].(string); !ok || v == "" {
				return true
			}
		}
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
