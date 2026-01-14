package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paulopiriquito/hog/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock logger for testing
type mockLogger struct {
	debugLogs    []string
	infoLogs     []string
	warningLogs  []string
	errorLogs    []string
	criticalLogs []string
	fatalLogs    []string
}

func (m *mockLogger) Debug(v ...interface{}) { m.debugLogs = append(m.debugLogs, fmt.Sprint(v...)) }
func (m *mockLogger) Info(v ...interface{})  { m.infoLogs = append(m.infoLogs, fmt.Sprint(v...)) }
func (m *mockLogger) Warning(v ...interface{}) {
	m.warningLogs = append(m.warningLogs, fmt.Sprint(v...))
}
func (m *mockLogger) Error(v ...interface{}) { m.errorLogs = append(m.errorLogs, fmt.Sprint(v...)) }
func (m *mockLogger) Critical(v ...interface{}) {
	m.criticalLogs = append(m.criticalLogs, fmt.Sprint(v...))
}
func (m *mockLogger) Fatal(v ...interface{}) { m.fatalLogs = append(m.fatalLogs, fmt.Sprint(v...)) }

func (m *mockLogger) reset() {
	m.debugLogs = []string{}
	m.infoLogs = []string{}
	m.warningLogs = []string{}
	m.errorLogs = []string{}
	m.criticalLogs = []string{}
	m.fatalLogs = []string{}
}

// Helper to create a valid config
func createValidConfig() PluginConfig {
	return PluginConfig{
		Idp: Idp{
			Type:         "oidc",
			Issuer:       "http://localhost:5556",
			WellKnown:    "/.well-known/openid-configuration",
			ClientId:     "test-client",
			ClientSecret: "test-secret",
		},
		Config: Config{
			SimpleAuthUrl:     "/oauth/simple-auth",
			TokenUrl:          "/oauth/token",
			CallbackUrl:       "/oauth/callback",
			LogoutUrl:         "/oauth/logout",
			UserInfoUrl:       "/oauth/userinfo",
			Scopes:            "openid profile email",
			SessionKey:        "12345678901234567890123456789012", // 32 bytes
			SessionCookieName: "auth_session",
		},
	}
}

// Helper to create mock OIDC discovery response
func createMockOIDCServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": "http://localhost:5556/auth",
				"token_endpoint":         "http://localhost:5556/token",
				"userinfo_endpoint":      "http://localhost:5556/userinfo",
				"jwks_uri":               "http://localhost:5556/keys",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestLoadPluginConfig(t *testing.T) {
	cfg := map[string]interface{}{
		"hog-authenticator": map[string]interface{}{
			"idp": map[string]interface{}{
				"type":          "oidc",
				"issuer":        "http://localhost:5556",
				"audience":      "http://localhost:8080",
				"well-known":    "/.well-known/openid-configuration",
				"client-id":     "test-client",
				"client-secret": "test-secret",
			},
			"config": map[string]interface{}{
				"simple-auth-url":     "/oauth/simple-auth",
				"token-url":           "/oauth/token",
				"callback-url":        "/oauth/callback",
				"logout-url":          "/oauth/logout",
				"user-info-url":       "/oauth/userinfo",
				"scopes":              "openid profile email",
				"session-key":         "12345678901234567890123456789012",
				"session-cookie-name": "auth_session",
			},
		},
	}

	pluginConfig, err := loadPluginConfig(cfg)
	assert.NoError(t, err)
	assert.Equal(t, "test-client", pluginConfig.Idp.ClientId)
	assert.Equal(t, "/oauth/simple-auth", pluginConfig.Config.SimpleAuthUrl)
}

func TestLoadPluginConfigInvalid(t *testing.T) {
	cfg := map[string]interface{}{
		"hog-authenticator": "invalid",
	}

	_, err := loadPluginConfig(cfg)
	assert.Error(t, err)
}

func TestDiscoverOIDCEndpoints(t *testing.T) {
	server := createMockOIDCServer()
	defer server.Close()

	idp := Idp{
		Issuer:    server.URL,
		WellKnown: "/.well-known/openid-configuration",
	}

	config, err := discoverOIDCEndpoints(idp)
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Contains(t, config.AuthorizationEndpoint, "/auth")
	assert.Contains(t, config.TokenEndpoint, "/token")
	assert.Contains(t, config.UserinfoEndpoint, "/userinfo")
}

func TestDiscoverOIDCEndpointsFailure(t *testing.T) {
	idp := Idp{
		Issuer:    "http://non-existent-server-12345.local",
		WellKnown: "/.well-known/openid-configuration",
	}

	_, err := discoverOIDCEndpoints(idp)
	assert.Error(t, err)
}

func TestGeneratePKCEVerifier(t *testing.T) {
	verifier, err := generatePKCEVerifier()
	assert.NoError(t, err)
	assert.NotEmpty(t, verifier)
	assert.GreaterOrEqual(t, len(verifier), 43) // Base64URL encoded 32 bytes
}

func TestGeneratePKCEChallenge(t *testing.T) {
	verifier := "test-verifier-123"
	challenge := generatePKCEChallenge(verifier)
	assert.NotEmpty(t, challenge)
	assert.Equal(t, 43, len(challenge)) // SHA256 base64url encoded
}

func TestGenerateState(t *testing.T) {
	state1 := generateState()
	state2 := generateState()
	assert.NotEmpty(t, state1)
	assert.NotEmpty(t, state2)
	assert.NotEqual(t, state1, state2)
}

func TestSessionCookieEncryptionDecryption(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"
	sessionData := session.SessionData{
		JWT:       "test.jwt.token",
		SessionID: "session-123",
	}

	encrypted, err := session.EncryptSessionCookie(sessionData, sessionKey)
	assert.NoError(t, err)
	assert.NotEmpty(t, encrypted)

	decrypted, err := session.DecryptSessionCookie(encrypted, sessionKey)
	assert.NoError(t, err)
	assert.Equal(t, sessionData.JWT, decrypted.JWT)
	assert.Equal(t, sessionData.SessionID, decrypted.SessionID)
}

func TestSessionCookieDecryptionInvalidKey(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"
	wrongKey := "00000000000000000000000000000000"
	sessionData := session.SessionData{
		JWT:       "test.jwt.token",
		SessionID: "session-123",
	}

	encrypted, err := session.EncryptSessionCookie(sessionData, sessionKey)
	assert.NoError(t, err)

	_, err = session.DecryptSessionCookie(encrypted, wrongKey)
	assert.Error(t, err)
}

func TestSessionCookieDecryptionInvalidData(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"

	_, err := session.DecryptSessionCookie("invalid-base64!", sessionKey)
	assert.Error(t, err)

	_, err = session.DecryptSessionCookie("dG9vc2hvcnQ=", sessionKey)
	assert.Error(t, err)
}

func TestExtractSubFromJWT(t *testing.T) {
	// Valid JWT with sub claim (eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyIsIm5hbWUiOiJUZXN0IFVzZXIifQ.signature)
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyIsIm5hbWUiOiJUZXN0IFVzZXIifQ.signature"
	sub := extractSubFromJWT(jwt)
	assert.Equal(t, "user-123", sub)
}

func TestExtractSubFromJWTInvalid(t *testing.T) {
	sub := extractSubFromJWT("invalid.jwt")
	assert.Equal(t, "unknown", sub)

	sub = extractSubFromJWT("invalid")
	assert.Equal(t, "unknown", sub)
}

func TestExtractUserClaimsFromJWT(t *testing.T) {
	// Create a JWT with all user claims
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-123","email":"user@example.com","name":"John Doe"}`))
	jwt := header + "." + payload + ".signature"

	claims := session.ExtractUserClaimsFromJWT(jwt)
	assert.Equal(t, "user-123", claims.Sub)
	assert.Equal(t, "user@example.com", claims.Email)
	assert.Equal(t, "John Doe", claims.Name)
}

func TestExtractUserClaimsFromJWTMissingClaims(t *testing.T) {
	// Create a JWT with only sub claim (missing email and name)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-123"}`))
	jwt := header + "." + payload + ".signature"

	claims := session.ExtractUserClaimsFromJWT(jwt)
	assert.Equal(t, "user-123", claims.Sub)
	assert.Equal(t, "", claims.Email)
	assert.Equal(t, "", claims.Name)
}

func TestHandleSimpleAuthNotAuthenticated(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	server := createMockOIDCServer()
	defer server.Close()

	config := createValidConfig()
	config.Idp.Issuer = server.URL

	discoveredConfig, err := discoverOIDCEndpoints(config.Idp)
	require.NoError(t, err)
	oidcConfig = discoveredConfig

	req := httptest.NewRequest(http.MethodGet, "/oauth/simple-auth", nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()

	handleSimpleAuth(config, rr, req)

	assert.Equal(t, http.StatusFound, rr.Code)
	location := rr.Header().Get("Location")
	assert.Contains(t, location, "code_challenge")
	assert.Contains(t, location, "code_challenge_method=S256")
	assert.Contains(t, location, "state=")

	// Verify logging
	assert.Greater(t, len(mockLog.infoLogs), 0)
	assert.Contains(t, mockLog.infoLogs[0], "Starting simple-auth flow")
}

func TestHandleSimpleAuthAlreadyAuthenticated(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	// Create encrypted session cookie
	sessionData := session.SessionData{
		JWT:       "test.jwt.token",
		SessionID: "session-123",
	}
	encryptedCookie, err := session.EncryptSessionCookie(sessionData, config.Config.SessionKey)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/oauth/simple-auth", nil)
	req.AddCookie(&http.Cookie{
		Name:  "auth_session",
		Value: encryptedCookie,
	})
	rr := httptest.NewRecorder()

	handleSimpleAuth(config, rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response map[string]string
	err = json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	// JWT should NOT be exposed - only status message
	assert.Equal(t, "authenticated", response["status"])
	assert.Equal(t, "Already authenticated", response["message"])
	assert.Empty(t, response["id_token"]) // Ensure JWT is not exposed

	// Verify logging
	assert.Contains(t, mockLog.infoLogs[0], "User already authenticated")
}

func TestHandleCallback(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	// Create mock token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id_token":      "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyIsIm5hbWUiOiJUZXN0IFVzZXIifQ.signature",
				"access_token":  "access-token-123",
				"refresh_token": "refresh-token-123",
			})
			return
		}
	}))
	defer tokenServer.Close()

	config := createValidConfig()

	// Set up OIDC config
	oidcConfig = &OIDCConfig{
		TokenEndpoint: tokenServer.URL + "/token",
	}

	// Create stateless state with verifier
	verifier := "test-verifier"
	state, err := createStatelessState(verifier, config.Config.SessionKey)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state="+state, nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()

	handleCallback(config, rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Check that cookie was set
	cookies := rr.Result().Cookies()
	assert.Greater(t, len(cookies), 0)
	assert.Equal(t, "auth_session", cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)

	// Verify logging
	assert.Contains(t, mockLog.infoLogs[0], "Token exchange successful")
	assert.Contains(t, mockLog.infoLogs[0], "sub=user-123")
}

func TestGetCallbackURLWithProxy(t *testing.T) {
	// Test with X-Forwarded-Proto and X-Forwarded-Host headers (ingress/proxy scenario)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	req.Host = "krakend-internal:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "api.example.com")

	url := getCallbackURL(req, "/oauth/callback")
	assert.Equal(t, "https://api.example.com/oauth/callback", url)
}

func TestGetCallbackURLWithoutProxy(t *testing.T) {
	// Test without proxy headers (direct connection)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	req.Host = "localhost:8080"

	url := getCallbackURL(req, "/oauth/callback")
	assert.Equal(t, "http://localhost:8080/oauth/callback", url)
}

func TestGetCallbackURLPartialProxy(t *testing.T) {
	// Test with only X-Forwarded-Proto (partial proxy config)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	req.Host = "krakend:8080"
	req.Header.Set("X-Forwarded-Proto", "https")

	url := getCallbackURL(req, "/oauth/callback")
	assert.Equal(t, "https://krakend:8080/oauth/callback", url)
}

func TestGetCallbackURLProxyOrigin(t *testing.T) {
	// Test with only Origin header (proxy origin scenario)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	req.Host = "krakend:8080"
	req.Header.Set("Origin", "https://api.example.com")

	url := getCallbackURL(req, "/oauth/callback")
	assert.Equal(t, "https://api.example.com/oauth/callback", url)
}

func TestHandleCallbackMissingCode(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=test-state", nil)
	rr := httptest.NewRecorder()

	handleCallback(config, rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, mockLog.warningLogs[0], "missing code or state")
}

func TestHandleCallbackInvalidState(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=invalid-state", nil)
	rr := httptest.NewRecorder()

	handleCallback(config, rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, mockLog.warningLogs[0], "Invalid or expired state")
}

func TestHandleTokenExchange(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	// Create mock token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":      "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyIsIm5hbWUiOiJUZXN0IFVzZXIifQ.signature",
			"access_token":  "access-token-123",
			"refresh_token": "refresh-token-123",
		})
	}))
	defer tokenServer.Close()

	config := createValidConfig()

	oidcConfig = &OIDCConfig{
		TokenEndpoint: tokenServer.URL,
	}

	requestBody := map[string]string{
		"code":          "test-code",
		"code_verifier": "test-verifier",
	}
	body, _ := json.Marshal(requestBody)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(string(body)))
	req.Host = "localhost:8080"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handleTokenExchange(config, rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Check cookie
	cookies := rr.Result().Cookies()
	assert.Greater(t, len(cookies), 0)
	assert.Equal(t, "auth_session", cookies[0].Name)

	// Verify logging
	assert.Contains(t, mockLog.infoLogs[0], "Token exchange successful")
}

func TestHandleTokenExchangeInvalidMethod(t *testing.T) {
	config := createValidConfig()

	req := httptest.NewRequest(http.MethodGet, "/oauth/token", nil)
	rr := httptest.NewRecorder()

	handleTokenExchange(config, rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleUserInfo(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	// Create mock userinfo endpoint
	userinfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":   "user-123",
			"name":  "Test User",
			"email": "test@example.com",
		})
	}))
	defer userinfoServer.Close()

	config := createValidConfig()

	oidcConfig = &OIDCConfig{
		UserinfoEndpoint: userinfoServer.URL,
	}

	// Create encrypted session cookie
	sessionData := session.SessionData{
		JWT:       "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyIsIm5hbWUiOiJUZXN0IFVzZXIifQ.signature",
		SessionID: "session-123",
	}
	encryptedCookie, err := session.EncryptSessionCookie(sessionData, config.Config.SessionKey)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	req.AddCookie(&http.Cookie{
		Name:  "auth_session",
		Value: encryptedCookie,
	})
	rr := httptest.NewRecorder()

	handleUserInfo(config, rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response map[string]interface{}
	err = json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "user-123", response["sub"])
	assert.Equal(t, "Test User", response["name"])

	// Verify logging
	assert.Contains(t, mockLog.infoLogs[0], "Userinfo retrieved")
}

func TestHandleUserInfoNoCookie(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	req := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	rr := httptest.NewRecorder()

	handleUserInfo(config, rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleLogout(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	// Create encrypted session cookie
	sessionData := session.SessionData{
		JWT:       "test.jwt.token",
		SessionID: "session-123",
	}
	encryptedCookie, err := session.EncryptSessionCookie(sessionData, config.Config.SessionKey)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/oauth/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  "auth_session",
		Value: encryptedCookie,
	})
	rr := httptest.NewRecorder()

	handleLogout(config, rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Check that cookie was cleared
	cookies := rr.Result().Cookies()
	assert.Greater(t, len(cookies), 0)
	assert.Equal(t, -1, cookies[0].MaxAge)

	// Verify logging
	assert.Contains(t, mockLog.infoLogs[0], "Logout initiated")
	assert.Contains(t, mockLog.infoLogs[1], "Logout completed")
}

func TestInjectAuthorizationHeader(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	config := createValidConfig()

	// Create a JWT with user claims (sub, email, name)
	// JWT format: header.payload.signature
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-123","email":"user@example.com","name":"Test User"}`))
	testJWT := header + "." + payload + ".signature"

	// Create encrypted session cookie
	sessionData := session.SessionData{
		JWT:       testJWT,
		Identity:  testJWT,
		SessionID: "session-123",
	}
	encryptedCookie, err := session.EncryptSessionCookie(sessionData, config.Config.SessionKey)
	require.NoError(t, err)

	// Mock next handler
	nextCalled := false
	var capturedAuthHeader, capturedUserId, capturedUserEmail, capturedUserName string
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		capturedAuthHeader = r.Header.Get("Authorization")
		capturedUserId = r.Header.Get("X-User-Id")
		capturedUserEmail = r.Header.Get("X-User-Email")
		capturedUserName = r.Header.Get("X-User-Name")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected-endpoint", nil)
	req.AddCookie(&http.Cookie{
		Name:  "auth_session",
		Value: encryptedCookie,
	})
	rr := httptest.NewRecorder()

	injectAuthorizationHeader(config, rr, req, nextHandler)

	assert.True(t, nextCalled)
	assert.Equal(t, "Bearer "+testJWT, capturedAuthHeader)
	assert.Equal(t, "user-123", capturedUserId)
	assert.Equal(t, "user@example.com", capturedUserEmail)
	assert.Equal(t, "Test User", capturedUserName)
	assert.Contains(t, mockLog.debugLogs[0], "Injected auth headers for session_id=")
}

func TestInjectAuthorizationHeaderNoCookie(t *testing.T) {
	config := createValidConfig()

	nextCalled := false
	var capturedAuthHeader string
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		capturedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected-endpoint", nil)
	rr := httptest.NewRecorder()

	injectAuthorizationHeader(config, rr, req, nextHandler)

	assert.True(t, nextCalled)
	assert.Equal(t, "", capturedAuthHeader) // No auth header injected
}

func TestLoggingDoesNotContainSensitiveData(t *testing.T) {
	mockLog := &mockLogger{}
	logger = mockLog

	// Create mock token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":      "secret-jwt-token",
			"access_token":  "secret-access-token",
			"refresh_token": "secret-refresh-token",
		})
	}))
	defer tokenServer.Close()

	config := createValidConfig()

	oidcConfig = &OIDCConfig{
		TokenEndpoint: tokenServer.URL,
	}

	// Create stateless state with verifier
	verifier := "secret-verifier-123"
	state, err := createStatelessState(verifier, config.Config.SessionKey)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=secret-code-456&state="+state, nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()

	handleCallback(config, rr, req)

	// Check all logs
	allLogs := append(mockLog.debugLogs, mockLog.infoLogs...)
	allLogs = append(allLogs, mockLog.warningLogs...)
	allLogs = append(allLogs, mockLog.errorLogs...)

	// Verify no sensitive data in logs
	for _, log := range allLogs {
		assert.NotContains(t, log, "secret-jwt-token")
		assert.NotContains(t, log, "secret-access-token")
		assert.NotContains(t, log, "secret-refresh-token")
		assert.NotContains(t, log, "secret-verifier-123")
		assert.NotContains(t, log, "secret-code-456")
	}

	// Verify audit data IS present
	found := false
	for _, log := range mockLog.infoLogs {
		if strings.Contains(log, "session_id=") && strings.Contains(log, "Token exchange successful") {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected to find audit log with session_id")
}

func TestRegisterLogger(t *testing.T) {
	testRegisterer := registerer(pluginName)
	mockLog := &mockLogger{}

	testRegisterer.RegisterLogger(mockLog)
	assert.NotNil(t, logger)
}

func TestRegisterLoggerInvalidType(t *testing.T) {
	testRegisterer := registerer(pluginName)
	originalLogger := logger

	testRegisterer.RegisterLogger("not-a-logger")
	assert.Equal(t, originalLogger, logger)
}

func TestCreateStatelessState(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"
	verifier := "test-verifier-123"

	state, err := createStatelessState(verifier, sessionKey)
	assert.NoError(t, err)
	assert.NotEmpty(t, state)

	// Should contain a dot separator (encoded.signature)
	assert.Contains(t, state, ".")
	parts := strings.Split(state, ".")
	assert.Equal(t, 2, len(parts))
}

func TestVerifyStatelessState(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"
	verifier := "test-verifier-123"

	state, err := createStatelessState(verifier, sessionKey)
	require.NoError(t, err)

	// Verify the state
	data, err := verifyStatelessState(state, sessionKey)
	assert.NoError(t, err)
	assert.Equal(t, verifier, data.Verifier)
	assert.NotEmpty(t, data.Nonce)
	assert.Greater(t, data.ExpiresAt, time.Now().Unix())
}

func TestVerifyStatelessStateInvalidSignature(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"
	wrongKey := "00000000000000000000000000000000"
	verifier := "test-verifier-123"

	state, err := createStatelessState(verifier, sessionKey)
	require.NoError(t, err)

	// Try to verify with wrong key
	_, err = verifyStatelessState(state, wrongKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid state signature")
}

func TestVerifyStatelessStateInvalidFormat(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"

	// Invalid format (no dot)
	_, err := verifyStatelessState("invalid-state-token", sessionKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid state format")

	// Invalid format (too many parts)
	_, err = verifyStatelessState("part1.part2.part3", sessionKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid state format")
}

func TestVerifyStatelessStateExpired(t *testing.T) {
	sessionKey := "12345678901234567890123456789012"

	// Create expired state manually
	data := StatelessStateData{
		Verifier:  "test-verifier",
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(), // Expired 1 hour ago
		Nonce:     uuid.New().String(),
	}

	jsonData, _ := json.Marshal(data)
	encoded := base64.RawURLEncoding.EncodeToString(jsonData)

	h := hmac.New(sha256.New, []byte(sessionKey))
	h.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	expiredState := encoded + "." + signature

	// Try to verify expired state
	_, err := verifyStatelessState(expiredState, sessionKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "state expired")
}
