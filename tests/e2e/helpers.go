// Package e2e drives HOG's docker-compose e2e stack (Caddy + dex + backend +
// hog-bff + hog-static) with real HTTP requests and a headless Chrome
// (chromedp), asserting the actual browser-facing behavior end to end.
//
// Bring the stack up first:
//
//	docker compose -f tests/e2e/docker-compose.yaml up --build -d
//
// then run (from this directory):
//
//	GOWORK=off go test -v -timeout 600s .
//
// or simply `make e2e` from the repo root, which brings the stack up, runs
// the suite, and tears it down.
package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	appOrigin           = "https://app.hog.localhost:8443"
	staticOrigin        = "https://static.hog.localhost:8443"
	sessionCookiePrefix = "hog_session."
)

// loopbackDialer rewrites connections to any *.hog.localhost:8443 host to
// 127.0.0.1:8443, so host-side Go assertions need no /etc/hosts edits: Chrome
// resolves "*.localhost" to loopback natively (per the WHATWG/browser
// convention), but net/http's DialContext does a real DNS lookup and would
// otherwise fail to resolve these names.
func loopbackDialer(ctx context.Context, network_, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil && strings.HasSuffix(host, ".hog.localhost") && port == "8443" {
		addr = "127.0.0.1:8443"
	}
	return (&net.Dialer{}).DialContext(ctx, network_, addr)
}

// baseTransport is shared by every httpClient variant: it dials *.hog.localhost
// at loopback and skips TLS verification (Caddy's `tls internal` self-signed cert).
func baseTransport() *http.Transport {
	return &http.Transport{
		DialContext:     loopbackDialer,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only, Caddy's self-signed dev cert
	}
}

// httpClient returns an unauthenticated client that does NOT follow redirects
// (CheckRedirect returns ErrUseLastResponse), so callers can assert directly on
// a 302/401/403 rather than whatever it points to.
func httpClient() *http.Client {
	return &http.Client{
		Transport:     baseTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// httpClientFollowingRedirects returns an unauthenticated client that follows
// redirects normally (Go's default policy), for assertions on the ultimate
// destination of a redirect chain rather than the redirect itself.
func httpClientFollowingRedirects() *http.Client {
	return &http.Client{Transport: baseTransport()}
}

// browser returns a headless chromedp context (ignoring the self-signed cert)
// and its cancel function; callers must defer cancel().
func browser(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("ignore-certificate-errors", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	return ctx, func() { cancelCtx(); cancelAlloc() }
}

// login drives dex's mockCallback connector to completion: it navigates to
// HOG's login endpoint with return_to=/app and waits for the browser to land
// back on app.hog.localhost:8443/app, authenticated as kilgore@kilgore.trout
// (groups: ["authors"]). mockCallback auto-redirects; there is no login form.
func login(ctx context.Context) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(appOrigin+"/auth/login?return_to=/app")); err != nil {
		return err
	}
	return waitLocation(ctx, appOrigin+"/app", 15*time.Second)
}

// waitLocation polls the current page URL until it equals want or the budget
// elapses, returning the last error/observed URL on timeout.
func waitLocation(ctx context.Context, want string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var loc string
	var err error
	for time.Now().Before(deadline) {
		if err = chromedp.Run(ctx, chromedp.Location(&loc)); err == nil && loc == want {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err == nil {
		err = context.DeadlineExceeded
	}
	return &locationTimeoutError{want: want, got: loc, err: err}
}

type locationTimeoutError struct {
	want, got string
	err       error
}

func (e *locationTimeoutError) Error() string {
	return "timed out waiting for location " + e.want + ", last seen " + e.got + ": " + e.err.Error()
}

// sessionCookie extracts the HOG session cookie chunks (hog_session.0, .1, …)
// from the chromedp browser context via the CDP Network domain, for reuse by
// a Go http.Client (see authenticatedClient).
func sessionCookie(ctx context.Context) ([]*network.Cookie, error) {
	var all []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetCookies().WithURLs([]string{appOrigin + "/"}).Do(ctx)
		if err != nil {
			return err
		}
		all = cookies
		return nil
	}))
	if err != nil {
		return nil, err
	}
	var sess []*network.Cookie
	for _, c := range all {
		if strings.HasPrefix(c.Name, sessionCookiePrefix) {
			sess = append(sess, c)
		}
	}
	return sess, nil
}

// uaTransport forces a fixed User-Agent on every request. HOG binds the
// session cookie to a server-side fingerprint computed over configured
// request headers (default: User-Agent only — see session/fingerprint.go);
// a Go http.Client presenting a different User-Agent than the browser that
// created the session would fail that check and read back 401. Forcing the
// same UA the browser used lets a plain net/http client reuse a chromedp-
// established session.
type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(req)
}

// authenticatedClient builds an httpClient() that carries the session cookie
// currently held by the browser at ctx (see sessionCookie) and presents the
// same User-Agent the browser used, so it round-trips as the same principal
// for JSON/backend assertions without needing to drive everything through
// in-page fetch(). ctx must already be past a successful login.
func authenticatedClient(ctx context.Context) (*http.Client, error) {
	cookies, err := sessionCookie(ctx)
	if err != nil {
		return nil, err
	}
	var ua string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`navigator.userAgent`, &ua)); err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(appOrigin + "/")
	httpCookies := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		httpCookies = append(httpCookies, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	jar.SetCookies(u, httpCookies)
	return &http.Client{
		Jar:           jar,
		Transport:     &uaTransport{base: baseTransport(), ua: ua},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// waitReady polls the public app origin until Caddy + hog-bff answer with a
// non-5xx status, up to a 60s budget. It returns whether the stack came up.
func waitReady(c *http.Client) bool {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := c.Get(appOrigin + "/"); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

// requireStack skips the test with a clear message when the compose stack
// isn't reachable, so the suite fails fast and legibly instead of timing out
// deep inside chromedp or a dozen individual requests.
func requireStack(t *testing.T) *http.Client {
	t.Helper()
	c := httpClient()
	if !waitReady(c) {
		t.Skip("e2e stack not reachable at " + appOrigin + " — run `docker compose -f tests/e2e/docker-compose.yaml up --build -d` (or `make e2e`) first")
	}
	return c
}

// --- container log helpers (mirror v1/tests/e2e's containerLogs/assertLogContains) ---

// composeProject is the docker-compose.yaml `name:`.
const composeProject = "hog-e2e"

// resolveContainer returns the container runtime (docker or podman, whichever
// can see the container) and the compose-generated name for service, covering
// both docker-compose (hyphen-joined, e.g. "hog-e2e-hog-bff-1") and
// podman-compose (underscore-joined, e.g. "hog-e2e_hog-bff_1") naming — and
// environments (like this one) where the `docker` CLI is itself a Podman
// compatibility shim. If no candidate is found (stack down), it returns an
// installed CLI and the docker-compose-style name so containerLogs degrades
// gracefully with a clear "could not start" log instead of a panic.
func resolveContainer(service string) (cli, name string) {
	candidates := []string{
		composeProject + "-" + service + "-1", // docker compose v2
		composeProject + "_" + service + "_1", // podman-compose
	}
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
func containerLogs(t *testing.T, service string, since time.Time) (*strings.Builder, func()) {
	t.Helper()
	cli, name := resolveContainer(service)
	buf := &strings.Builder{}
	var mu sync.Mutex

	cmd := exec.Command(cli, "logs", "--follow", "--since", since.UTC().Format(time.RFC3339), name)
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
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// assertLogContains polls buf for up to 10s looking for substr, failing the
// test if it never appears.
func assertLogContains(t *testing.T, buf *strings.Builder, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("log line not found: expected log to contain %q within 10s\nactual log:\n%s", substr, buf.String())
}
