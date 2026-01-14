package session

import (
	"net/http"
	"time"
)

const DefaultCookieName = "auth_session"

// CookieConfig holds configuration for session cookie management
type CookieConfig struct {
	CookieName string
	SessionKey string
}

// GetCookieName returns the cookie name with fallback to default
func GetCookieName(cookieName string) string {
	if cookieName != "" {
		return cookieName
	}
	return DefaultCookieName
}

// CalculateMaxAge calculates the session max age from JWT expiration claim
func CalculateMaxAge(jwt string) int {
	claims := ExtractUserClaimsFromJWT(jwt)
	if claims.Exp == 0 {
		// Default to 1 hour if no expiration
		return 3600
	}
	maxAge := claims.Exp - time.Now().Unix()
	if maxAge < 0 {
		return 0
	}
	return int(maxAge)
}

// isSecureRequest determines if the request is over HTTPS
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// SetSessionCookie creates and sets an encrypted session cookie
func SetSessionCookie(
	w http.ResponseWriter, r *http.Request, config CookieConfig, idToken string, accessToken string, sessionID string,
) error {
	sessionData := SessionData{
		JWT:       accessToken,
		Identity:  idToken,
		SessionID: sessionID,
	}

	encryptedCookie, err := EncryptSessionCookie(sessionData, config.SessionKey)
	if err != nil {
		return err
	}

	maxAge := CalculateMaxAge(accessToken)

	http.SetCookie(w, &http.Cookie{
		Name:     GetCookieName(config.CookieName),
		Value:    encryptedCookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})

	return nil
}

// ClearSessionCookie clears the session cookie
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, cookieName string) {
	http.SetCookie(w, &http.Cookie{
		Name:     GetCookieName(cookieName),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// GetSessionFromCookie retrieves and decrypts the session from the cookie
func GetSessionFromCookie(r *http.Request, config CookieConfig) (*SessionData, error) {
	cookie, err := r.Cookie(GetCookieName(config.CookieName))
	if err != nil {
		return nil, err
	}
	return DecryptSessionCookie(cookie.Value, config.SessionKey)
}
