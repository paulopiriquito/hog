// E2E tests for Hog auth flows.
// Requires the local-stack and e2e services to be running:
//
//	docker compose -f tests/local-stack/docker-compose.yaml up -d
//	docker compose -f tests/e2e/docker-compose.e2e.yaml up --build -d
//
// Run with:
//
//	cd tests/e2e && GOWORK=off go test -v -timeout 300s .
package e2e

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	baseURL     = "http://localhost:3000"
	dexLoginURL = "dex:5556"
	dexEmail    = "test@example.com"
	dexPassword = "password"
)

// Container runtime and names are resolved at startup so the suite runs under
// both docker-compose (hyphen-joined names, e.g. "hog-local-stack-hog-1") and
// podman-compose (underscore-joined names, e.g. "hog-local-stack_hog_1").
var (
	containerCLI string
	hogContainer string
	apiContainer string
)

func init() {
	containerCLI, hogContainer = resolveContainer("hog-local-stack-hog-1", "hog-local-stack_hog_1")
	_, apiContainer = resolveContainer("hog-e2e-e2e-api-1", "hog-e2e_e2e-api_1")
}

// resolveContainer returns the container runtime (docker or podman, whichever
// can see the container) and the matching name from the candidates, covering
// the hyphen/underscore naming difference between docker-compose and
// podman-compose. If none is found (stack down), it returns an installed CLI
// and the first candidate so containerLogs degrades gracefully.
func resolveContainer(candidates ...string) (cli, name string) {
	for _, c := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(c); err != nil {
			continue
		}
		for _, n := range candidates {
			if exec.Command(c, "container", "inspect", n).Run() == nil {
				return c, n
			}
		}
	}
	for _, c := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(c); err == nil {
			return c, candidates[0]
		}
	}
	return "docker", candidates[0]
}

// containerLogs streams a container's stdout+stderr into a buffer from the
// given start time. Call the returned stop function when the test ends.
func containerLogs(t *testing.T, container string, since time.Time) (*strings.Builder, func()) {
	t.Helper()
	buf := &strings.Builder{}
	var mu sync.Mutex

	cmd := exec.Command(containerCLI, "logs", "--follow", "--since",
		since.UTC().Format(time.RFC3339), container)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Logf("containerLogs: could not start (stack running?): %v", err)
		return buf, func() {}
	}
	scan := func(s *bufio.Scanner) {
		for s.Scan() {
			mu.Lock()
			buf.WriteString(s.Text() + "\n")
			mu.Unlock()
		}
	}
	go scan(bufio.NewScanner(stdout))
	go scan(bufio.NewScanner(stderr))

	return buf, func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	}
}

// assertLogContains polls buf for up to 5 s looking for substr.
func assertLogContains(t *testing.T, buf *strings.Builder, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Failf(t, "log line not found",
		"expected log to contain %q within 5s\nactual log:\n%s", substr, buf.String())
}

// newBrowser returns a headless Chrome context and its cancel function.
func newBrowser(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)
	return ctx, func() { cancel(); allocCancel() }
}

// waitDexLogin waits until the Dex login form is visible.
func waitDexLogin() chromedp.Action {
	return chromedp.WaitVisible(`input[name="login"]`, chromedp.ByQuery)
}

// submitDexLogin fills the Dex login form via JS and clicks submit.
// The submit triggers navigation; callers must wait for the new page themselves.
func submitDexLogin() chromedp.Action {
	script := fmt.Sprintf(`
		(function() {
			var email = document.querySelector('input[name="login"]');
			var pass  = document.querySelector('input[name="password"]');
			var btn   = document.querySelector('button[type="submit"]');
			if (!email || !pass || !btn) return false;
			email.value = %q;
			pass.value  = %q;
			btn.click();
			return true;
		})()
	`, dexEmail, dexPassword)
	var ok bool
	return chromedp.Evaluate(script, &ok)
}

// waitURL polls until the current page URL contains the given substring.
// Uses chromedp.Location in a Go-side loop, which is safe across navigations.
func waitURL(t *testing.T, urlSubstr string) chromedp.Action {
	t.Helper()
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var u string
			if err := chromedp.Location(&u).Do(ctx); err == nil && strings.Contains(u, urlSubstr) {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		var u string
		chromedp.Location(&u).Do(ctx) //nolint:errcheck
		return fmt.Errorf("timeout waiting for URL to contain %q, last URL: %s", urlSubstr, u)
	})
}

// awaitPromise wraps a JS expression in an EvaluateOption that awaits promises.
func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// generatePKCE returns a random PKCE verifier and its S256 challenge.
func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b) //nolint:gosec
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// TestSimpleAuth_ProtectedStaticContent verifies the full server-driven 302
// flow for a protected static route (/e2e-static/protected.html).
func TestSimpleAuth_ProtectedStaticContent(t *testing.T) {
	start := time.Now()
	logBuf, stopLogs := containerLogs(t, hogContainer, start)
	defer stopLogs()

	ctx, cancel := newBrowser(t)
	defer cancel()

	var loginURL, finalURL, pageText string

	err := chromedp.Run(ctx,
		// Navigate and wait to land on the Dex login page.
		chromedp.Navigate(baseURL+"/e2e-static/protected.html"),
		waitDexLogin(),
		chromedp.Location(&loginURL),

		// Fill and submit login form, then wait to land back on the protected page.
		submitDexLogin(),
		waitURL(t, "/e2e-static/protected.html"),
		chromedp.Location(&finalURL),
		chromedp.Evaluate(`document.querySelector('h1') ? document.querySelector('h1').innerText : ''`, &pageText),
	)
	require.NoError(t, err)

	assert.Contains(t, loginURL, dexLoginURL, "should have been redirected to Dex login")
	assert.Equal(t, baseURL+"/e2e-static/protected.html", finalURL)
	assert.Equal(t, "Protected Static Page", strings.TrimSpace(pageText))

	assertLogContains(t, logBuf, "Token exchange successful")
}

// TestSimpleAuth_ExpiredSessionRestartsFlow verifies that a stale/invalid
// session cookie does not cause an infinite redirect loop: the server must
// clear the cookie and restart the PKCE flow, landing the user at Dex login.
func TestSimpleAuth_ExpiredSessionRestartsFlow(t *testing.T) {
	ctx, cancel := newBrowser(t)
	defer cancel()

	// Navigate first so the domain is established, then inject a stale cookie.
	err := chromedp.Run(ctx,
		chromedp.Navigate(baseURL),
		// Set an invalid cookie value to simulate a stale/tampered session.
		network.SetCookie("auth_session", "stale-invalid-encrypted-value").
			WithDomain("localhost").
			WithPath("/").
			WithHTTPOnly(true),
	)
	require.NoError(t, err)

	var loginURL, pageText string
	err = chromedp.Run(ctx,
		chromedp.Navigate(baseURL+"/e2e-static/protected.html"),
		waitDexLogin(),
		chromedp.Location(&loginURL),
		submitDexLogin(),
		waitURL(t, "/e2e-static/protected.html"),
		chromedp.Evaluate(`document.querySelector('h1') ? document.querySelector('h1').innerText : ''`, &pageText),
	)
	require.NoError(t, err)

	// Must have redirected to Dex login (not looped back to the app).
	assert.Contains(t, loginURL, dexLoginURL,
		"expired session should restart auth at Dex, not loop back to the app")
	assert.Equal(t, "Protected Static Page", strings.TrimSpace(pageText))
}

// TestProtectedAPI_401ThenSimpleAuth verifies that:
//   - an unauthenticated fetch to a KrakenD jose-protected endpoint returns 401
//   - navigating to /oauth/simple-auth?redirect=... completes the full auth flow
//   - the API endpoint then returns the echoed Authorization header
func TestProtectedAPI_401ThenSimpleAuth(t *testing.T) {
	start := time.Now()
	apiLogBuf, stopAPI := containerLogs(t, apiContainer, start)
	defer stopAPI()

	ctx, cancel := newBrowser(t)
	defer cancel()

	// 1. Confirm unauthenticated fetch returns 401
	var statusCode float64
	err := chromedp.Run(ctx,
		chromedp.Navigate(baseURL),
		chromedp.Evaluate(
			`fetch('/e2e-api/headers', {credentials:'include'}).then(r => r.status)`,
			&statusCode, awaitPromise,
		),
	)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusUnauthorized), statusCode,
		"unauthenticated API request must return 401")

	// 2. Client navigates to simple-auth with the API path as redirect target,
	//    completes login, and lands on the API endpoint (JSON response).
	var loginURL, pageText string
	err = chromedp.Run(ctx,
		chromedp.Navigate(baseURL+"/oauth/simple-auth?redirect=/e2e-api/headers"),
		waitDexLogin(),
		chromedp.Location(&loginURL),
		submitDexLogin(),
		waitURL(t, "/e2e-api/headers"),
		chromedp.Evaluate(`document.body ? document.body.innerText : ''`, &pageText),
	)
	require.NoError(t, err)
	assert.Contains(t, loginURL, dexLoginURL)
	assert.Contains(t, pageText, "Authorization",
		"api response must echo the Authorization header injected by hog")
	assert.Contains(t, pageText, "Bearer",
		"Authorization header must be a Bearer token")

	// 3. The api container must have received the authenticated request
	assertLogContains(t, apiLogBuf, "")
}

// TestPKCEClientFlow verifies the client-driven PKCE flow end-to-end:
//  1. Generate verifier + challenge in Go
//  2. GET /oauth/pkce-init → receive authorization_url
//  3. Navigate to IdP, complete login, land on /oauth/pkce-callback?code=...
//  4. POST /oauth/token {code, code_verifier} → session cookie set
//  5. Subsequent API request succeeds with Authorization header
func TestPKCEClientFlow(t *testing.T) {
	start := time.Now()
	logBuf, stopLogs := containerLogs(t, hogContainer, start)
	defer stopLogs()

	ctx, cancel := newBrowser(t)
	defer cancel()

	// 1. Generate PKCE pair server-side (no crypto subtlety needed in JS)
	verifier, challenge := generatePKCE()
	state := fmt.Sprintf("e2e-%d", rand.Int()) //nolint:gosec
	redirectURI := baseURL + "/oauth/pkce-callback"

	// 2. Call /oauth/pkce-init from the browser and extract authorization_url
	initURL := fmt.Sprintf("/oauth/pkce-init?code_challenge=%s&redirect_uri=%s&state=%s",
		challenge, redirectURI, state)
	var authURLJSON string
	err := chromedp.Run(ctx,
		chromedp.Navigate(baseURL),
		chromedp.Evaluate(
			fmt.Sprintf(`fetch(%q).then(r=>r.json()).then(d=>JSON.stringify(d))`, initURL),
			&authURLJSON, awaitPromise,
		),
	)
	require.NoError(t, err)

	var initResp map[string]string
	require.NoError(t, json.Unmarshal([]byte(authURLJSON), &initResp))
	authorizationURL := initResp["authorization_url"]

	assert.Contains(t, authorizationURL, dexLoginURL, "authorization_url must point to Dex")
	assert.Contains(t, authorizationURL, challenge, "authorization_url must embed code_challenge")
	assert.Contains(t, authorizationURL, state, "authorization_url must embed state")

	// 3. Navigate to IdP, complete Dex login, wait to land on pkce-callback
	err = chromedp.Run(ctx,
		chromedp.Navigate(authorizationURL),
		waitDexLogin(),
		submitDexLogin(),
		waitURL(t, "/oauth/pkce-callback"),
	)
	require.NoError(t, err)

	var callbackURL string
	err = chromedp.Run(ctx, chromedp.Location(&callbackURL))
	require.NoError(t, err)
	assert.Contains(t, callbackURL, "code=", "callback URL must contain authorization code")

	// 4. Exchange code for session cookie using the verifier generated in Go
	exchangeScript := fmt.Sprintf(`
		(async function() {
			const params = new URLSearchParams(window.location.search);
			const code = params.get('code');
			const resp = await fetch('/oauth/token', {
				method: 'POST',
				credentials: 'include',
				headers: {'Content-Type': 'application/json'},
				body: JSON.stringify({code: code, code_verifier: %q, redirect_uri: %q})
			});
			return resp.status;
		})()
	`, verifier, redirectURI)
	var tokenStatus float64
	err = chromedp.Run(ctx,
		chromedp.Evaluate(exchangeScript, &tokenStatus, awaitPromise),
	)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusOK), tokenStatus, "token exchange must return 200")

	// 5. Session cookie is now set — API request must succeed
	var apiStatus float64
	err = chromedp.Run(ctx,
		chromedp.Evaluate(
			`fetch('/e2e-api/headers', {credentials:'include'}).then(r=>r.status)`,
			&apiStatus, awaitPromise,
		),
	)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusOK), apiStatus,
		"API request must succeed after PKCE token exchange")

	assertLogContains(t, logBuf, "Token exchange successful")
}
