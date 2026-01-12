package session

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// UserClaims contains user information extracted from JWT
type UserClaims struct {
	Sub   string
	Email string
	Name  string
	Exp   int64
	Iat   int64
	Aud   interface{} // Can be string or []string
}

// ExtractUserClaimsFromJWT extracts user claims from the JWT payload
// Note: This does NOT validate the JWT signature - use for informational purposes only
func ExtractUserClaimsFromJWT(jwt string) UserClaims {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return UserClaims{Sub: "unknown", Email: "", Name: ""}
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return UserClaims{Sub: "unknown", Email: "", Name: ""}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return UserClaims{Sub: "unknown", Email: "", Name: ""}
	}

	result := UserClaims{
		Sub:   "unknown",
		Email: "",
		Name:  "",
	}

	if sub, ok := claims["sub"].(string); ok {
		result.Sub = sub
	}
	if email, ok := claims["email"].(string); ok {
		result.Email = email
	}
	if name, ok := claims["name"].(string); ok {
		result.Name = name
	}
	if exp, ok := claims["exp"].(float64); ok {
		result.Exp = int64(exp)
	}
	if iat, ok := claims["iat"].(float64); ok {
		result.Iat = int64(iat)
	}
	if aud, ok := claims["aud"]; ok {
		result.Aud = aud
	}

	return result
}

// IsJWTExpired checks if the JWT has expired based on the exp claim
// Note: This does NOT validate the JWT signature
func IsJWTExpired(jwt string) bool {
	claims := ExtractUserClaimsFromJWT(jwt)
	if claims.Exp == 0 {
		// No expiration claim, consider it expired for safety
		return true
	}
	return time.Now().Unix() > claims.Exp
}

// ValidateJWTBasic performs basic JWT validation without signature verification
// This checks format and expiration only - signature must be verified separately
func ValidateJWTBasic(jwt string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return errors.New("invalid JWT format: must have 3 parts")
	}

	// Decode and parse payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("invalid JWT format: cannot decode payload")
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return errors.New("invalid JWT format: cannot parse payload JSON")
	}

	// Check expiration
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return errors.New("JWT has expired")
		}
	}

	return nil
}
