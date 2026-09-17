package terminal

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/v2/auth"
	"github.com/paulopiriquito/hog/v2/session"
	"github.com/paulopiriquito/hog/v2/telemetry"
)

// sharedTransport is the verifying, connection-pooled transport reused by every
// proxy/api handler. A handler needing insecureSkipVerify gets its own (clone).
var sharedTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// backendRoundTripper carries every backend call through OTel client instrumentation.
var backendRoundTripper = telemetry.InstrumentedTransport(sharedTransport)

// insecureTransport clones the shared transport with TLS verification disabled.
func insecureTransport() *http.Transport {
	t := sharedTransport.Clone()
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // caller opts in explicitly
	return t
}

// forwardOptions controls what a backend request carries.
type forwardOptions struct {
	forwardAccessToken bool // inject Authorization: Bearer from the session principal's access token
	forwardCookies     bool // pass the inbound Cookie header through (default: strip)
	forwardIdentity    bool // pass the inbound identity assertion header through (default: strip)
}

// prepareBackendRequest mutates the outbound backend request out using the inbound
// request in and opts: strips the Cookie header (unless forwardCookies), forwards
// the projected X-User-* identity headers, unconditionally removes any inbound
// Authorization (so a client-supplied bearer never leaks to the backend), then
// injects Authorization from the session principal only when forwardAccessToken is
// set and a principal with an access token exists. Sets X-Forwarded-*. The access
// token is never logged.
func prepareBackendRequest(out, in *http.Request, opts forwardOptions) {
	if !opts.forwardCookies {
		out.Header.Del("Cookie")
	}
	for k, vs := range in.Header {
		if strings.HasPrefix(k, "X-User-") { // http.Header keys are canonical
			out.Header[k] = append([]string(nil), vs...)
		}
	}
	// Authorization to the backend is controlled solely by HOG: any inbound
	// (possibly client-supplied) Authorization is removed, then the session
	// principal's access token is injected only when forwardAccessToken is set.
	// This keeps a client-injected bearer from leaking to the backend and makes
	// forwardAccessToken the single source of a backend bearer token. The token
	// is never logged.
	out.Header.Del("Authorization")
	if opts.forwardAccessToken {
		if p, ok := session.FromContext(in.Context()); ok && p.AccessToken != "" {
			out.Header.Set("Authorization", "Bearer "+p.AccessToken)
		}
	}
	// The identity assertion (minted by IssueAssertion on the inbound request) reaches
	// the backend only when the route opts in; otherwise it is dropped like Authorization.
	// IssueAssertion may have minted it under a configured, non-default header name
	// (identity.assertion.issue.header) — this handler never sees that gateway
	// config, only the per-route forwardIdentity flag, so the name it actually used
	// is read back off the request context. Both the default name and the
	// context-supplied one (when they differ) are stripped, so neither a stale
	// default-named header nor a client-supplied one under the configured name can
	// reach the backend.
	assertionHeader := auth.DefaultAssertionHeader
	if h := auth.AssertionHeaderFromContext(in.Context()); h != "" {
		assertionHeader = h
	}
	out.Header.Del(auth.DefaultAssertionHeader)
	if assertionHeader != auth.DefaultAssertionHeader {
		out.Header.Del(assertionHeader)
	}
	if opts.forwardIdentity {
		if v := in.Header.Get(assertionHeader); v != "" {
			out.Header.Set(assertionHeader, v)
		}
	}
	setXForwarded(out, in)
}

// setXForwarded maintains the X-Forwarded-* chain. HOG sits behind a
// TLS-terminating LB, so X-Forwarded-Proto comes from the inbound value, not r.TLS.
// This trusts the LB to be the sole ingress and to set X-Forwarded-Proto; a directly-exposed HOG instance would let a client spoof it.
func setXForwarded(out, in *http.Request) {
	if ip := clientIP(in); ip != "" {
		if prior := in.Header.Get("X-Forwarded-For"); prior != "" {
			out.Header.Set("X-Forwarded-For", prior+", "+ip)
		} else {
			out.Header.Set("X-Forwarded-For", ip)
		}
	}
	if in.Host != "" {
		out.Header.Set("X-Forwarded-Host", in.Host)
	}
	if proto := in.Header.Get("X-Forwarded-Proto"); proto != "" {
		out.Header.Set("X-Forwarded-Proto", proto)
	}
}

// clientIP returns the immediate peer IP (host part of RemoteAddr).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
