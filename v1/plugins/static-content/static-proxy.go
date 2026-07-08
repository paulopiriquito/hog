package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/pkg/headers"
	"github.com/paulopiriquito/hog/pkg/paths"
	"github.com/paulopiriquito/hog/pkg/session"
)

func handleStaticContent(config PluginConfig, w http.ResponseWriter, req *http.Request, h http.Handler) {
	// Always skip OAuth routes - hardcoded for security
	if strings.HasPrefix(req.URL.Path, "/oauth/") {
		h.ServeHTTP(w, req)
		return
	}

	if matchesGatewayEndpoints(config.ServiceGateway, req.URL.Path) {
		h.ServeHTTP(w, req) //Continue with the gateway chain
		return
	}

	if matchedStatic := matchesStaticContentEndpoint(config.Static, req.URL.Path); matchedStatic.ServiceHost != "" {
		// Check if authentication is required for this route
		if matchedStatic.Auth {
			if !isAuthenticated(config, req) {
				logger.Debug(fmt.Sprintf("Authentication required for %s, redirecting to auth", req.URL.Path))
				redirectToAuth(config, w, req)
				return
			}
			logger.Debug(fmt.Sprintf("Authenticated request for %s", req.URL.Path))
		}

		logger.Debug(fmt.Sprintf("Handling static content request for %s", req.URL.Path))
		proxyToStaticServer(matchedStatic, w, req)
		return
	}

	h.ServeHTTP(w, req)
}

func proxyToStaticServer(staticServer StaticConfig, w http.ResponseWriter, req *http.Request) {
	targetURL, err := url.Parse(staticServer.ServiceHost)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to parse service host: %s", err.Error()))
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(outReq *http.Request) {
			outReq.URL.Scheme = targetURL.Scheme
			outReq.URL.Host = targetURL.Host
			outReq.Host = targetURL.Host
			outReq.URL.Path = req.URL.Path
			outReq.URL.RawQuery = req.URL.RawQuery

			if staticServer.KeepUnsafeHeaders {
				headers.CopyAll(outReq, req)
			} else {
				headers.CopyAllSecure(outReq, req)
			}
			headers.SetProxyHeaders(outReq, req)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error(fmt.Sprintf("proxy error: %s", err.Error()))
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	startTime := time.Now()
	proxy.ServeHTTP(w, req)
	logger.Debug(fmt.Sprintf("Proxied request %v", map[string]string{
		"method":     req.Method,
		"path":       req.URL.Path,
		"host":       req.Host,
		"user_agent": req.UserAgent(),
		"latency":    time.Since(startTime).String(),
	}))
}

func matchesStaticContentEndpoint(config []StaticConfig, path string) StaticConfig {
	for _, s := range config {
		if paths.MatchesWildcard(path, s.PathPrefix) {
			return s
		}
	}
	return StaticConfig{}
}

func matchesGatewayEndpoints(config ServiceGatewayConfig, path string) bool {
	if paths.ExistsInPaths(path, config.PathPrefix) {
		return true // matches at least one gateway endpoint
	}
	return false
}

// isAuthenticated checks if the request has a valid session cookie with a non-expired JWT
func isAuthenticated(config PluginConfig, req *http.Request) bool {
	cookieName := config.Auth.SessionCookieName
	if cookieName == "" {
		cookieName = "auth_session"
	}

	cookie, err := req.Cookie(cookieName)
	if err != nil {
		logger.Debug(fmt.Sprintf("No session cookie found: %v", err))
		return false
	}

	// If no session key configured, cannot validate - just check cookie exists
	if config.Auth.SessionKey == "" {
		logger.Warning("Session key not configured, cannot validate JWT expiration")
		return cookie.Value != ""
	}

	// Decrypt session cookie
	sessionData, err := session.DecryptSessionCookie(cookie.Value, config.Auth.SessionKey)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to decrypt session cookie: %v", err))
		return false
	}

	// Validate JWT is not expired
	if err := session.ValidateJWTBasic(sessionData.JWT); err != nil {
		logger.Debug(fmt.Sprintf("JWT validation failed: %v", err))
		return false
	}

	logger.Debug(fmt.Sprintf("Valid session found for session_id=%s", sessionData.SessionID))
	return true
}

// redirectToAuth redirects the user to the authentication flow
func redirectToAuth(config PluginConfig, w http.ResponseWriter, req *http.Request) {
	authUrl := config.Auth.SimpleAuthUrl
	if authUrl == "" {
		authUrl = "/oauth/simple-auth"
	}

	// Build redirect URL with return path
	redirectURL := fmt.Sprintf("%s?redirect=%s", authUrl, url.QueryEscape(req.URL.Path))
	http.Redirect(w, req, redirectURL, http.StatusFound)
}
