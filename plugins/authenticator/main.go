package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/paulopiriquito/hog/pkg/forward"
	"github.com/paulopiriquito/hog/pkg/headers"
	"github.com/paulopiriquito/hog/pkg/pluginlogger"
	"github.com/paulopiriquito/hog/pkg/session"
)

var pluginName = "hog-authenticator"

var HandlerRegisterer = registerer(pluginName)

type registerer string

func (r registerer) RegisterHandlers(f func(
	name string,
	handler func(context.Context, map[string]interface{}, http.Handler) (http.Handler, error),
)) {
	f(string(r), r.registerHandlers)
}

func main() {}

/*
Configuration for hog-authenticator plugin:

Environment Variables (take precedence over config file):
  - IDP_ISSUER: OIDC provider URL (e.g., "http://dex:5556")
  - IDP_CLIENT_ID: OAuth client ID
  - IDP_CLIENT_SECRET: OAuth client secret
  - AUTH_COOKIE_KEY: 32-byte session encryption key
  - AUTH_COOKIE_NAME: Session cookie name (default: "auth_session")

Minimal configuration (when using environment variables):
"extra_config": {
	"plugin/http-server": {
		"name":["hog-authenticator"],
		"hog-authenticator": {
			"idp": {
				"type": "oidc"
			}
		}
	}
}

Full configuration (all options):
"extra_config": {
	"plugin/http-server": {
		"name":["hog-authenticator"],
		"hog-authenticator": {
			"idp": {
				"type": "oidc",
				"issuer": "http://dex:5556",
				"well-known": "/.well-known/openid-configuration",
				"client-id": "my-client-id",
				"client-secret": "my-client-secret"
			},
			"config": {
				"simple-auth-url": "/oauth/simple-auth",
				"token-url": "/oauth/token",
				"logout-url": "/oauth/logout",
				"user-info-url": "/oauth/userinfo",
				"callback-url": "/oauth/callback",
				"scopes": "openid profile email",
				"session-key": "my-secret-key-32-bytes-long!",
				"session-cookie-name": "auth_session"
			}
		}
	}
}

Defaults:
  - well-known: /.well-known/openid-configuration
  - simple-auth-url: /oauth/simple-auth
  - token-url: /oauth/token
  - callback-url: /oauth/callback
  - logout-url: /oauth/logout
  - user-info-url: /oauth/userinfo
  - scopes: openid profile email
  - session-cookie-name: auth_session

Note: Simple-auth flow uses stateless PKCE with signed state tokens for horizontal scaling support.
*/

// OIDC Discovery endpoints
type OIDCConfig struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
}

var oidcConfig *OIDCConfig

// The authenticator plugin will look for this configuration:
func (r registerer) registerHandlers(ctx context.Context, extra map[string]interface{}, h http.Handler) (http.Handler, error) {
	logger.Debug(fmt.Sprintf("Loading authenticator plugin config"))
	config, err := loadPluginConfig(extra)
	if err != nil {
		return nil, errors.Join(errors.New("failed to load plugin config"), err)
	}

	// Validate session key length (must be 32 bytes for AES-256)
	if len(config.Config.SessionKey) != 32 {
		return nil, fmt.Errorf("session-key must be exactly 32 bytes for AES-256")
	}

	// Discover OIDC endpoints
	logger.Debug(fmt.Sprintf("Discovering OIDC endpoints from IdP: %s", config.Idp.Issuer))
	oidcConfig, err = discoverOIDCEndpoints(config.Idp)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to discover OIDC endpoints: %v", err))
		return nil, fmt.Errorf("failed to discover OIDC endpoints: %w", err)
	}
	logger.Info(fmt.Sprintf("OIDC discovery successful - authorization_endpoint=%s", oidcConfig.AuthorizationEndpoint))

	logger.Debug(fmt.Sprintf("Registering authenticator routes (stateless mode)"))

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path

		// Auth routes
		if path == config.Config.SimpleAuthUrl {
			handleSimpleAuth(config, writer, request)
			return
		}
		if path == config.Config.TokenUrl {
			handleTokenExchange(config, writer, request)
			return
		}
		if path == config.Config.CallbackUrl {
			handleCallback(config, writer, request)
			return
		}
		if path == config.Config.LogoutUrl {
			handleLogout(config, writer, request)
			return
		}
		if path == config.Config.UserInfoUrl {
			handleUserInfo(config, writer, request)
			return
		}

		// For all other requests, try to inject Authorization header from session cookie
		injectAuthorizationHeader(config, writer, request, h)
	}), nil
}

type PluginConfig struct {
	Idp     Idp            `mapstructure:"idp"`
	Config  Config         `mapstructure:"config"`
	Forward forward.Config `mapstructure:"forward"`
}

type Idp struct {
	Type         string `mapstructure:"type"`
	Issuer       string `mapstructure:"issuer"`
	WellKnown    string `mapstructure:"well-known"`
	ClientId     string `mapstructure:"client-id"`
	ClientSecret string `mapstructure:"client-secret"`
}

type Config struct {
	SimpleAuthUrl     string `mapstructure:"simple-auth-url"`
	TokenUrl          string `mapstructure:"token-url"`
	CallbackUrl       string `mapstructure:"callback-url"`
	LogoutUrl         string `mapstructure:"logout-url"`
	UserInfoUrl       string `mapstructure:"user-info-url"`
	Scopes            string `mapstructure:"scopes"`
	SessionKey        string `mapstructure:"session-key"`
	SessionCookieName string `mapstructure:"session-cookie-name"`
}

func loadPluginConfig(cfg map[string]interface{}) (PluginConfig, error) {
	var pc PluginConfig
	err := mapstructure.Decode(cfg[pluginName], &pc)
	if err != nil {
		return pc, fmt.Errorf("failed to decode config: %w", err)
	}

	// Environment variables take precedence over config file
	// Priority: ENV VAR > Config File > Defaults

	// IDP Configuration from environment
	if envIssuer := os.Getenv("IDP_ISSUER"); envIssuer != "" {
		pc.Idp.Issuer = envIssuer
	}
	if envClientId := os.Getenv("IDP_CLIENT_ID"); envClientId != "" {
		pc.Idp.ClientId = envClientId
	}
	if envClientSecret := os.Getenv("IDP_CLIENT_SECRET"); envClientSecret != "" {
		pc.Idp.ClientSecret = envClientSecret
	}

	// IDP defaults
	if pc.Idp.WellKnown == "" {
		pc.Idp.WellKnown = "/.well-known/openid-configuration"
	}
	if pc.Idp.Type == "" {
		pc.Idp.Type = "oidc"
	}

	// Config URL defaults
	if pc.Config.SimpleAuthUrl == "" {
		pc.Config.SimpleAuthUrl = "/oauth/simple-auth"
	}
	if pc.Config.TokenUrl == "" {
		pc.Config.TokenUrl = "/oauth/token"
	}
	if pc.Config.CallbackUrl == "" {
		pc.Config.CallbackUrl = "/oauth/callback"
	}
	if pc.Config.LogoutUrl == "" {
		pc.Config.LogoutUrl = "/oauth/logout"
	}
	if pc.Config.UserInfoUrl == "" {
		pc.Config.UserInfoUrl = "/oauth/userinfo"
	}
	if pc.Config.Scopes == "" {
		pc.Config.Scopes = "openid profile email"
	}

	// Session Cookie Name
	if envCookieName := os.Getenv("AUTH_COOKIE_NAME"); envCookieName != "" {
		pc.Config.SessionCookieName = envCookieName
	} else if pc.Config.SessionCookieName == "" {
		pc.Config.SessionCookieName = "auth_session"
	}

	// Session Key
	if envSessionKey := os.Getenv("AUTH_COOKIE_KEY"); envSessionKey != "" {
		pc.Config.SessionKey = envSessionKey
	} else if pc.Config.SessionKey == "" {
		// Generate a random 32-byte key and set it as environment variable
		randomKey, err := session.GenerateRandomKey()
		if err != nil {
			return pc, fmt.Errorf("failed to generate random session key: %w", err)
		}
		pc.Config.SessionKey = randomKey
		os.Setenv("AUTH_COOKIE_KEY", randomKey)
		logger.Warning("No session key configured, generated random key and set AUTH_COOKIE_KEY environment variable")
	}

	if len(pc.Forward.Headers) > 0 {
		if err := pc.Forward.Validate(); err != nil {
			return pc, fmt.Errorf("invalid forward config: %w", err)
		}
	}

	return pc, nil
}

// OIDC Discovery with retry mechanism
const (
	oidcDiscoveryMaxRetries     = 5
	oidcDiscoveryInitialBackoff = 2 * time.Second
)

func discoverOIDCEndpoints(idp Idp) (*OIDCConfig, error) {
	wellKnownURL := strings.TrimSuffix(idp.Issuer, "/") + idp.WellKnown

	var lastErr error
	backoff := oidcDiscoveryInitialBackoff

	for attempt := 1; attempt <= oidcDiscoveryMaxRetries; attempt++ {
		config, err := fetchOIDCConfig(wellKnownURL)
		if err == nil {
			if attempt > 1 {
				logger.Info(fmt.Sprintf("OIDC discovery succeeded on attempt %d/%d", attempt, oidcDiscoveryMaxRetries))
			}
			return config, nil
		}

		lastErr = err
		if attempt < oidcDiscoveryMaxRetries {
			logger.Warning(fmt.Sprintf("OIDC discovery attempt %d/%d failed: %v. Retrying in %v...", attempt, oidcDiscoveryMaxRetries, err, backoff))
			time.Sleep(backoff)
			backoff *= 2 // Exponential backoff
		}
	}
	logger.Fatal(fmt.Sprintf("OIDC discovery failed after %d attempts. Last error: %v. Terminating.", oidcDiscoveryMaxRetries, lastErr))
	return nil, lastErr
}

func fetchOIDCConfig(wellKnownURL string) (*OIDCConfig, error) {
	resp, err := http.Get(wellKnownURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch well-known config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("well-known endpoint returned status %d", resp.StatusCode)
	}

	var config OIDCConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode well-known config: %w", err)
	}

	return &config, nil
}

// PKCE Utilities
func generatePKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generatePKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func generateState() string {
	return uuid.New().String()
}

// Stateless State Token (for horizontal scaling)
type StatelessStateData struct {
	Verifier     string `json:"verifier"`
	ExpiresAt    int64  `json:"exp"`
	Nonce        string `json:"nonce"`
	RedirectPath string `json:"redirect_path,omitempty"`
}

func createStatelessState(verifier string, key string) (string, error) {
	return createStatelessStateWithRedirect(verifier, "", key)
}

func createStatelessStateWithRedirect(verifier string, redirectPath string, key string) (string, error) {
	data := StatelessStateData{
		Verifier:     verifier,
		ExpiresAt:    time.Now().Add(10 * time.Minute).Unix(),
		Nonce:        uuid.New().String(),
		RedirectPath: redirectPath,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	// Encode data
	encoded := base64.RawURLEncoding.EncodeToString(jsonData)

	// Create HMAC signature
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Return signed token: encoded.signature
	return encoded + "." + signature, nil
}

func verifyStatelessState(signedState string, key string) (*StatelessStateData, error) {
	parts := strings.Split(signedState, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid state format")
	}

	encoded := parts[0]
	providedSignature := parts[1]

	// Verify signature
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(encoded))
	expectedSignature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(expectedSignature), []byte(providedSignature)) {
		return nil, errors.New("invalid state signature")
	}

	// Decode data
	jsonData, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	var data StatelessStateData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, err
	}

	// Check expiration
	if time.Now().Unix() > data.ExpiresAt {
		return nil, errors.New("state expired")
	}

	return &data, nil
}

// Session Cookie Encryption/Decryption - using shared session package

// Handler: Simple Auth (server-generated PKCE)
func handleSimpleAuth(config PluginConfig, w http.ResponseWriter, r *http.Request) {
	// Check for redirect parameter (from static-content plugin)
	redirectPath := r.URL.Query().Get("redirect")

	// Check if already authenticated
	sessionData, err := session.GetSessionFromCookie(r, getCookieConfig(config))
	if err == nil && sessionData.JWT != "" {
		logger.Info(fmt.Sprintf("User already authenticated session_id=%s", sessionData.SessionID))

		// If redirect parameter is present, redirect to that path
		if redirectPath != "" {
			logger.Debug(fmt.Sprintf("Redirecting authenticated user to %s", redirectPath))
			http.Redirect(w, r, redirectPath, http.StatusFound)
			return
		}

		// Return authenticated status without exposing the JWT
		sendJSONResponse(w, http.StatusOK, map[string]string{
			"status":  "authenticated",
			"message": "Already authenticated",
		})
		return
	}
	if err == nil {
		logger.Debug("Existing session cookie invalid or expired")
	}

	// Generate PKCE parameters
	verifier, err := generatePKCEVerifier()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to generate PKCE verifier: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	challenge := generatePKCEChallenge(verifier)
	sessionID := uuid.New().String()

	// Create stateless signed state token with embedded verifier and optional redirect
	state, err := createStatelessStateWithRedirect(verifier, redirectPath, config.Config.SessionKey)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to create stateless state: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Info(fmt.Sprintf("Starting simple-auth flow session_id=%s", sessionID))

	// Build authorization URL
	authURL, err := url.Parse(oidcConfig.AuthorizationEndpoint)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to parse authorization endpoint: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := authURL.Query()
	q.Set("client_id", config.Idp.ClientId)
	q.Set("response_type", "code")
	q.Set("scope", config.Config.Scopes)
	q.Set("redirect_uri", getCallbackURL(r, config.Config.CallbackUrl))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	logger.Debug(fmt.Sprintf("Redirecting to IdP authorization endpoint"))
	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

// Handler: Callback (OAuth redirect)
func handleCallback(config PluginConfig, w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	logger.Debug(fmt.Sprintf("OAuth callback received"))

	if code == "" || state == "" {
		logger.Warning("OAuth callback missing code or state")
		http.Error(w, "Invalid callback parameters", http.StatusBadRequest)
		return
	}

	// Extract verifier from stateless signed state token
	stateData, err := verifyStatelessState(state, config.Config.SessionKey)
	if err != nil {
		logger.Warning(fmt.Sprintf("Invalid or expired state: %v", err))
		http.Error(w, "Invalid or expired state", http.StatusBadRequest)
		return
	}
	verifier := stateData.Verifier
	logger.Debug("State verified successfully")

	// Exchange code for token
	sessionID := uuid.New().String()
	token, err := exchangeCodeForToken(r.Context(), config, code, verifier, getCallbackURL(r, config.Config.CallbackUrl))
	if err != nil {
		logger.Error(fmt.Sprintf("Token exchange failed session_id=%s error=%v", sessionID, err))
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}

	idToken := token["id_token"].(string)
	accessToken := token["access_token"].(string)

	// Extract sub from JWT for audit logging
	sub := extractSubFromJWT(idToken)
	sessionMaxAge := session.CalculateMaxAge(accessToken)
	logger.Info(fmt.Sprintf("Token exchange successful sub=%s session_id=%s session_max_age=%d", sub, sessionID, sessionMaxAge))

	fwdHeaders, _, fwdErr := computeForwardHeaders(r.Context(), accessToken, sessionID, config.Forward)
	if fwdErr != nil {
		logger.Warning(fmt.Sprintf("forward.userinfo fetch failed during login session_id=%s error=%v", sessionID, fwdErr))
	}

	// Encrypt and set cookie
	if err := session.SetSessionCookie(w, r, getCookieConfig(config), session.Data{
		JWT:       accessToken,
		Identity:  idToken,
		SessionID: sessionID,
		Headers:   fwdHeaders,
	}); err != nil {
		logger.Error(fmt.Sprintf("Failed to set session cookie: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Debug(fmt.Sprintf("Session cookie set session_id=%s", sessionID))

	// Check if we should redirect to original path (from static-content plugin)
	if stateData.RedirectPath != "" {
		logger.Debug(fmt.Sprintf("Redirecting to original path: %s", stateData.RedirectPath))
		http.Redirect(w, r, stateData.RedirectPath, http.StatusFound)
		return
	}

	// Otherwise return JSON response
	sendJSONResponse(w, http.StatusOK, map[string]string{
		"status":  "authenticated",
		"message": "Authentication successful",
	})
}

// Handler: Token Exchange (PKCE from SPA)
func handleTokenExchange(config PluginConfig, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warning("Invalid token exchange request body")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sessionID := uuid.New().String()
	logger.Debug(fmt.Sprintf("PKCE token exchange initiated session_id=%s", sessionID))

	// Exchange code for token
	token, err := exchangeCodeForToken(r.Context(), config, req.Code, req.CodeVerifier, getCallbackURL(r, config.Config.CallbackUrl))
	if err != nil {
		logger.Error(fmt.Sprintf("Token exchange failed session_id=%s error=%v", sessionID, err))
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}

	idToken := token["id_token"].(string)
	accessToken := token["access_token"].(string)

	sub := extractSubFromJWT(idToken)
	sessionMaxAge := session.CalculateMaxAge(accessToken)
	logger.Info(fmt.Sprintf("Token exchange successful sub=%s session_id=%s session_max_age=%d", sub, sessionID, sessionMaxAge))

	fwdHeaders, _, fwdErr := computeForwardHeaders(r.Context(), accessToken, sessionID, config.Forward)
	if fwdErr != nil {
		logger.Warning(fmt.Sprintf("forward.userinfo fetch failed during login session_id=%s error=%v", sessionID, fwdErr))
	}

	// Encrypt and set cookie
	if err := session.SetSessionCookie(w, r, getCookieConfig(config), session.Data{
		JWT:       accessToken,
		Identity:  idToken,
		SessionID: sessionID,
		Headers:   fwdHeaders,
	}); err != nil {
		logger.Error(fmt.Sprintf("Failed to set session cookie: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	sendJSONResponse(w, http.StatusOK, map[string]string{
		"status":  "authenticated",
		"message": "Authentication successful",
	})
}

// Handler: User Info
func handleUserInfo(config PluginConfig, w http.ResponseWriter, r *http.Request) {
	sessionData, err := session.GetSessionFromCookie(r, getCookieConfig(config))
	if err != nil {
		logger.Debug("No session cookie found for userinfo request")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	logger.Debug(fmt.Sprintf("Userinfo request session_id=%s", sessionData.SessionID))

	rawBody, parsed, err := fetchUserInfoRaw(r.Context(), sessionData.JWT)
	if err != nil {
		logger.Warning(fmt.Sprintf("Userinfo fetch failed session_id=%s error=%v", sessionData.SessionID, err))
		session.ClearSessionCookie(w, r, config.Config.SessionCookieName)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sub := extractSubFromJWT(sessionData.Identity)
	logger.Info(fmt.Sprintf("Userinfo retrieved sub=%s session_id=%s", sub, sessionData.SessionID))

	// Pass-through mode when forward is not configured.
	if len(config.Forward.Headers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(rawBody)
		return
	}

	// Forward mode: enrich response, refresh cookie Headers.
	if parsed == nil {
		// Body was not JSON — cannot enrich. Pass through.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(rawBody)
		return
	}

	res := forward.Apply(parsed, config.Forward)
	emitForwardDiagnostics(res.Diagnostics, sessionData.SessionID)

	// Refresh cookie with new Headers.
	if err := session.SetSessionCookie(w, r, getCookieConfig(config), session.Data{
		JWT:       sessionData.JWT,
		Identity:  sessionData.Identity,
		SessionID: sessionData.SessionID,
		Headers:   res.Headers,
	}); err != nil {
		logger.Error(fmt.Sprintf("Failed to refresh session cookie: %v", err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Enriched response: raw IdP fields + mapped object.
	enriched := make(map[string]any, len(parsed)+1)
	for k, v := range parsed {
		enriched[k] = v
	}
	enriched["mapped"] = res.Mapped

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(enriched); err != nil {
		logger.Error(fmt.Sprintf("Failed to encode enriched userinfo: %v", err))
	}
}

// Handler: Logout
func handleLogout(config PluginConfig, w http.ResponseWriter, r *http.Request) {
	sessionID := "unknown"
	sessionData, err := session.GetSessionFromCookie(r, getCookieConfig(config))
	if err == nil {
		sessionID = sessionData.SessionID
	}

	logger.Info(fmt.Sprintf("Logout initiated session_id=%s", sessionID))

	session.ClearSessionCookie(w, r, config.Config.SessionCookieName)

	logger.Info(fmt.Sprintf("Logout completed session_id=%s", sessionID))

	sendJSONResponse(w, http.StatusOK, map[string]string{
		"status":  "logged_out",
		"message": "Logout successful",
	})
}

// Middleware: Inject Authorization Header
func injectAuthorizationHeader(config PluginConfig, w http.ResponseWriter, r *http.Request, next http.Handler) {
	cookieName := session.GetCookieName(config.Config.SessionCookieName)

	cookie, err := r.Cookie(cookieName)
	if err != nil {
		next.ServeHTTP(w, r)
		return
	}
	sessionData, err := session.DecryptSessionCookie(cookie.Value, config.Config.SessionKey)
	if err != nil {
		logger.Debug(fmt.Sprintf("Cookie decryption failed: %v", err))
		next.ServeHTTP(w, r)
		return
	}

	r.Header.Set("Authorization", "Bearer "+sessionData.JWT)
	r.Header.Set("Identity", sessionData.Identity)

	if len(config.Forward.Headers) > 0 {
		// Allowlist mode: emit configured headers from session.
		for name, value := range sessionData.Headers {
			r.Header.Set(name, value)
		}
		logger.Debug(fmt.Sprintf("Injected forward headers count=%d session_id=%s path=%s",
			len(sessionData.Headers), sessionData.SessionID, r.URL.Path))
	} else {
		// Legacy mode: identity headers from id_token JWT.
		userClaims := session.ExtractUserClaimsFromJWT(sessionData.Identity)
		if userClaims.Sub != "" && userClaims.Sub != "unknown" {
			r.Header.Set("X-User-Id", userClaims.Sub)
		}
		if userClaims.Email != "" {
			r.Header.Set("X-User-Email", userClaims.Email)
		}
		if userClaims.Name != "" {
			r.Header.Set("X-User-Name", userClaims.Name)
		}
		logger.Debug(fmt.Sprintf("Injected legacy identity headers session_id=%s path=%s",
			sessionData.SessionID, r.URL.Path))
	}

	next.ServeHTTP(w, r)
}

// Helper: Exchange authorization code for token
func exchangeCodeForToken(ctx context.Context, config PluginConfig, code, verifier, redirectURI string) (map[string]interface{}, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", config.Idp.ClientId)
	data.Set("client_secret", config.Idp.ClientSecret)
	data.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oidcConfig.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Inject trace headers for distributed tracing
	traceFormat := os.Getenv("TRACE_FORMAT")
	if traceFormat == "" {
		traceFormat = headers.TraceFormatOTEL
	}
	headers.InjectTraceHeaders(ctx, req, traceFormat)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// Helper: Extract sub from JWT (for audit logging only, no validation)
// Using shared session package for JWT parsing

func extractSubFromJWT(jwt string) string {
	claims := session.ExtractUserClaimsFromJWT(jwt)
	return claims.Sub
}

// computeForwardHeaders fetches userinfo from the IdP and applies the forward
// config. Returns the wire headers map (suitable for storing in the session
// cookie) and a non-nil Result for callers that need the mapped view. On any
// failure, returns nil headers and the error — callers decide whether to
// continue without forwarding.
func computeForwardHeaders(ctx context.Context, accessToken, sessionID string, cfg forward.Config) (map[string]string, forward.Result, error) {
	if len(cfg.Headers) == 0 {
		return nil, forward.Result{}, nil
	}
	userinfo, err := fetchUserInfo(ctx, accessToken)
	if err != nil {
		return nil, forward.Result{}, err
	}
	res := forward.Apply(userinfo, cfg)
	emitForwardDiagnostics(res.Diagnostics, sessionID)
	logger.Info(fmt.Sprintf("forward.headers extracted session_id=%s headers_emitted=%d", sessionID, len(res.Headers)))
	return res.Headers, res, nil
}

func emitForwardDiagnostics(diags []forward.Diagnostic, sessionID string) {
	for _, d := range diags {
		switch d.Reason {
		case "missing_claim":
			logger.Debug(fmt.Sprintf("forward.header skipped reason=missing_claim claim=%s header=%s session_id=%s", d.Claim, d.Header, sessionID))
		case "wrong_type":
			logger.Warning(fmt.Sprintf("forward.header skipped reason=wrong_type claim=%s header=%s session_id=%s", d.Claim, d.Header, sessionID))
		case "no_matches":
			logger.Info(fmt.Sprintf("forward.header skipped reason=no_matches claim=%s header=%s samples=%v session_id=%s", d.Claim, d.Header, d.Samples, sessionID))
		}
	}
}

// Helper: Get cookie config from plugin config
func getCookieConfig(config PluginConfig) session.CookieConfig {
	return session.CookieConfig{
		CookieName: config.Config.SessionCookieName,
		SessionKey: config.Config.SessionKey,
	}
}

// Helper: Send JSON response
func sendJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// Helper: Get callback URL
func getCallbackURL(r *http.Request, callbackPath string) string {
	// Check for Origin header (from reverse proxy/ingress)
	origin := r.Header.Get("Origin")
	if origin != "" {
		return fmt.Sprintf("%s%s", origin, callbackPath)
	}

	// Check for X-Forwarded-Proto header (from reverse proxy/ingress)
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		// Fallback to direct TLS detection
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}

	// Check for X-Forwarded-Host header (from reverse proxy/ingress)
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		// Fallback to request Host
		host = r.Host
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, callbackPath)
}

// This logger is replaced by the RegisterLogger method to load the one from KrakenD
var logger pluginlogger.Logger = pluginlogger.NoopLogger{}

func (registerer) RegisterLogger(v interface{}) {
	pluginlogger.RegisterLogger(&logger, v, string(HandlerRegisterer))
}
