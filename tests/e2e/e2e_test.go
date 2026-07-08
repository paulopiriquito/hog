package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// sessionView mirrors session.PublicView's JSON shape (see session/session.go):
// the SPA-facing projection of the session — identity + TTL, never tokens.
type sessionView struct {
	Subject  string         `json:"subject"`
	Passport map[string]any `json:"passport"`
	Groups   []string       `json:"groups"`
}

// echoResponse mirrors the e2e backend's echo shape (see tests/e2e/backend/main.go):
// what HOG forwarded (identity headers, bearer presence, and whether the
// Cookie header was stripped before reaching the upstream).
type echoResponse struct {
	Path       string `json:"path"`
	UserID     string `json:"user_id"`
	UserGroups string `json:"user_groups"`
	HasBearer  bool   `json:"has_bearer"`
	Cookie     string `json:"cookie"`
}

// decodeJSON decodes and closes resp.Body into a T, failing the test on error.
func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return v
}

// TestStaticSPA covers hog-static (the public SPA host): the shell is served
// at "/", an extensionless deep link falls back to the SPA shell (client-side
// routing), and a missing asset (has an extension, so no fallback) 404s.
func TestStaticSPA(t *testing.T) {
	c := requireStack(t)

	resp, err := c.Get(staticOrigin + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "<title>hog e2e</title>")

	deepLink, err := c.Get(staticOrigin + "/some/route")
	require.NoError(t, err)
	deepLink.Body.Close()
	require.Equal(t, http.StatusOK, deepLink.StatusCode, "extensionless deep link must SPA-fallback to index.html")

	missing, err := c.Get(staticOrigin + "/missing.js")
	require.NoError(t, err)
	missing.Body.Close()
	require.Equal(t, http.StatusNotFound, missing.StatusCode, "a missing asset (with an extension) must not SPA-fallback")
}

// TestBFFLoginLogout drives the full dex mockCallback login through a real
// headless browser, checks the SPA + /auth/session reflect the authenticated
// identity, then exercises logout's method/origin restrictions: GET is not
// state-changing, a cross-origin POST is rejected by CSRF, and a same-origin
// POST clears the session.
func TestBFFLoginLogout(t *testing.T) {
	requireStack(t)

	ctx, cancel := browser(t)
	defer cancel()
	ctx, tcancel := context.WithTimeout(ctx, 90*time.Second)
	defer tcancel()

	require.NoError(t, login(ctx), "login via dex mockCallback")

	var status, who string
	require.NoError(t, chromedp.Run(ctx,
		chromedp.WaitVisible("status", chromedp.ByID),
		chromedp.Text("status", &status, chromedp.ByID),
		chromedp.Text("who", &who, chromedp.ByID),
	))
	require.Equal(t, "authenticated", status, "SPA #status must read 'authenticated' once logged in")
	require.NotEmpty(t, who, "SPA #who must show the session subject")

	client, err := authenticatedClient(ctx)
	require.NoError(t, err)

	sessResp, err := client.Get(appOrigin + "/auth/session")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sessResp.StatusCode)
	view := decodeJSON[sessionView](t, sessResp)
	require.Equal(t, who, view.Subject, "the DOM and the /auth/session JSON must agree on the subject")
	require.Contains(t, view.Groups, "authors", "dex mockCallback's kilgore carries groups:[authors]")
	require.Equal(t, "kilgore@kilgore.trout", view.Passport["email"])

	unauth := httpClient()

	// GET is not a registered method for "POST /auth/logout"; in THIS topology
	// (unlike an isolated auth-endpoints-only build) there is also a gateway-
	// wide "/" shell route registered as a catch-all subtree pattern, and Go's
	// net/http.ServeMux resolves an unmatched-method request to that less-
	// specific-but-still-matching pattern instead of auto-403/405ing — so a GET
	// here serves the SPA shell (200), not 405. The isolated method-restriction
	// (405, with no competing catch-all route) is unit-covered in
	// app/build_test.go: TestBuildLogoutRequiresPostSameOrigin. What this e2e
	// topology CAN and must prove is the actual security property: a GET must
	// never perform the state change.
	getLogout, err := unauth.Get(appOrigin + "/auth/logout")
	require.NoError(t, err)
	getLogout.Body.Close()
	require.Equal(t, http.StatusOK, getLogout.StatusCode)
	stillIn, err := client.Get(appOrigin + "/auth/session")
	require.NoError(t, err)
	stillIn.Body.Close()
	require.Equal(t, http.StatusOK, stillIn.StatusCode, "a GET on the logout path must not clear the session")

	// Cross-origin POST: net/http.CrossOriginProtection rejects it before the
	// logout handler ever runs (closes cross-site forced-logout).
	crossReq, err := http.NewRequest(http.MethodPost, appOrigin+"/auth/logout", nil)
	require.NoError(t, err)
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossResp, err := unauth.Do(crossReq)
	require.NoError(t, err)
	crossResp.Body.Close()
	require.Equal(t, http.StatusForbidden, crossResp.StatusCode)

	// Same-origin POST (the session cookie, no Sec-Fetch-Site/Origin — a plain
	// same-origin/non-browser request) proceeds and clears the session.
	logoutResp, err := client.Post(appOrigin+"/auth/logout", "", nil)
	require.NoError(t, err)
	logoutResp.Body.Close()
	require.True(t, logoutResp.StatusCode >= 200 && logoutResp.StatusCode < 400,
		"same-origin logout status=%d, want 2xx/3xx", logoutResp.StatusCode)

	afterLogout, err := client.Get(appOrigin + "/auth/session")
	require.NoError(t, err)
	afterLogout.Body.Close()
	require.Equal(t, http.StatusUnauthorized, afterLogout.StatusCode, "session must be cleared after logout")
}

// TestAuthzByGroup logs in as kilgore (groups:[authors]) and exercises the
// group-gated routes: /reports requires "authors" (ALLOW), /admin requires
// "admins" (DENY, kilgore doesn't carry it) — and the denial must be logged
// server-side.
func TestAuthzByGroup(t *testing.T) {
	requireStack(t)

	ctx, cancel := browser(t)
	defer cancel()
	ctx, tcancel := context.WithTimeout(ctx, 60*time.Second)
	defer tcancel()
	require.NoError(t, login(ctx))

	client, err := authenticatedClient(ctx)
	require.NoError(t, err)

	start := time.Now()
	logBuf, stopLogs := containerLogs(t, "hog-bff", start)
	defer stopLogs()

	reportsResp, err := client.Get(appOrigin + "/reports")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, reportsResp.StatusCode)
	echo := decodeJSON[echoResponse](t, reportsResp)
	require.Contains(t, echo.UserGroups, "authors")

	adminResp, err := client.Get(appOrigin + "/admin")
	require.NoError(t, err)
	adminResp.Body.Close()
	require.Equal(t, http.StatusForbidden, adminResp.StatusCode)

	assertLogContains(t, logBuf, "authz denied")
	assertLogContains(t, logBuf, "policy=admins")
}

// TestBackendAndSecurity covers the reverse-proxy identity projection
// (X-User-*, forwardAccessToken, cookie-stripping), the /agg aggregation
// handler, the gateway-wide security response headers, and cross-origin CSRF
// rejection.
func TestBackendAndSecurity(t *testing.T) {
	c := requireStack(t)

	headerResp, err := c.Get(appOrigin + "/")
	require.NoError(t, err)
	headerResp.Body.Close()
	require.Equal(t, "nosniff", headerResp.Header.Get("X-Content-Type-Options"))
	require.NotEmpty(t, headerResp.Header.Get("X-Frame-Options"))
	require.NotEmpty(t, headerResp.Header.Get("Strict-Transport-Security"))

	// Cross-origin unsafe (POST) request: rejected gateway-wide, pre-auth.
	csrfReq, err := http.NewRequest(http.MethodPost, appOrigin+"/app", nil)
	require.NoError(t, err)
	csrfReq.Header.Set("Sec-Fetch-Site", "cross-site")
	csrfResp, err := c.Do(csrfReq)
	require.NoError(t, err)
	csrfResp.Body.Close()
	require.Equal(t, http.StatusForbidden, csrfResp.StatusCode)

	ctx, cancel := browser(t)
	defer cancel()
	ctx, tcancel := context.WithTimeout(ctx, 60*time.Second)
	defer tcancel()
	require.NoError(t, login(ctx))
	client, err := authenticatedClient(ctx)
	require.NoError(t, err)

	apiResp, err := client.Get(appOrigin + "/api/whatever")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, apiResp.StatusCode)
	echo := decodeJSON[echoResponse](t, apiResp)
	require.NotEmpty(t, echo.UserID, "X-User-Id must be projected onto the backend request")
	require.True(t, echo.HasBearer, "forwardAccessToken must attach a Bearer token")
	require.Empty(t, echo.Cookie, "HOG must strip the Cookie header before proxying to the backend")

	aggResp, err := client.Get(appOrigin + "/agg")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, aggResp.StatusCode)
	agg := decodeJSON[map[string]map[string]int](t, aggResp)
	require.Equal(t, 1, agg["one"]["v"])
	require.Equal(t, 2, agg["two"]["v"])
}
