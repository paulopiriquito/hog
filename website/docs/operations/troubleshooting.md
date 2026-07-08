# Troubleshooting

Common symptoms, their likely cause, and where to fix them.

## Redirect loop / stuck on a protected route

**Symptom:** an `app`-typed route (`auth: required`) keeps redirecting to
the login path, or the login path itself 404s.

- **Cause: `session` is configured without an `IdP`.** HOG logs a startup
  warning (`session configured without an IdP: protected routes will
  redirect to the login path, but the auth endpoints are not mounted`) and
  keeps running — the auth gate still redirects unauthenticated requests to
  `loginPath`, but `loginPath` itself is never mounted, so the browser lands
  on a `404`. Fix: add a `kind: IdP` resource, or drop `session` if you
  don't need authentication. See [authentication](authentication.md).
- **Cause: the session fingerprint never matches after login.** If
  `session.fingerprintHeaders` includes a header a proxy normalizes or
  strips between the callback response and the next request (or your
  client legitimately changes it, e.g. a browser auto-update mid-session),
  every read fails and the user is bounced back to login immediately. Check
  what's actually configured (default is just `User-Agent`) and what your
  ingress does to headers.
- **Cause: cookies aren't reaching the browser at all.** See
  [cookies not set](#cookies-not-set-or-not-sent-back) below — a redirect
  loop is what a missing cookie looks like from the user's side.

## 403 forbidden from a policy

Authorization denies are **generic by design**: the response body is always
`forbidden`, with no policy name or reason. Look in the logs, not the
response:

- A denied request logs `authz denied` (or `authz denied (policy error)`
  for a Rego evaluation failure) at `Info`/`Error`, with the policy name,
  the reason, the subject, the route, and — when tracing is active — the
  `trace_id`/`span_id`.
- The same information is attached to the request's trace span as an
  `authz.deny` event.

If the deny is unexpected, check: does the principal actually have the
required group/claim (`GET` the session-info endpoint to see its public
view), and does a `rego` policy's `deny` rule assume an `input` shape that
doesn't match what's actually sent (see
[authorization: what's in `input`](authorization.md#whats-in-input))? See
[authorization](authorization.md#how-a-decision-is-reached) for the
full evaluation model (deny-overrides, fail-closed).

## 502/504 from a backend

- **`502` (bad gateway)** — the `reverse-proxy` or `api` backend request
  failed for any reason other than the configured timeout (connection
  refused, DNS failure, TLS handshake failure, non-2xx-and-invalid-JSON
  body for an `api` backend). Check the backend is actually reachable from
  where HOG runs, and that `insecureSkipVerify` is set if it's using a
  self-signed cert you trust.
- **`504` (gateway timeout)** — the request exceeded `handler.timeout`.
  Raise the timeout, or investigate why the backend is slow.
- **A partial `200` with `X-Hog-Partial`** — for an `api` route, a backend
  marked `required: false` failed or timed out; its group is listed in
  `X-Hog-Partial` and simply omitted from the merged JSON rather than
  failing the whole request. This is expected behavior, not an error — a
  `required: true` (the default) backend failing is what produces a
  `502`/`504` instead.

See [configuration: handler types](configuration.md#handler-types).

## Static `404` / SPA fallback not working

- A request for an **extensionless** path (no `.` in the last segment) that
  doesn't resolve to a real file falls back to `index` (`spaFallback`,
  default `true`) — this is what makes client-side routing work.
- A request for a path **with an extension** (e.g. a missing
  `/assets/app.abc123.js`) does **not** fall back — a genuinely missing
  asset is always a real `404`, not the SPA shell. If you're seeing the
  SPA shell served for a missing asset instead of a `404`, check the
  request path actually has no extension (some build tools emit
  extensionless chunk names).
- Any path segment starting with `.` (a dotfile, or `..`) is always
  rejected with `404`, independent of `spaFallback` — this can't be
  disabled.
- If the server **fails to start** rather than 404ing, `handler.dir` itself
  doesn't exist or isn't readable — the static handler opens its root
  directory once, fail-fast, at boot.

See [configuration: `static`](configuration.md#static).

## OIDC callback failures

The callback endpoint returns a specific status/body per failure — check
which one you're getting:

| Response | Meaning | Likely cause |
|---|---|---|
| `400 login session expired; please restart sign-in` | No `hog_login` cookie on the callback request. | The login attempt is more than 10 minutes old, the browser dropped the cookie, or the user opened the callback URL directly/replayed it. |
| `400 login session invalid; please restart sign-in` | The `hog_login` cookie failed to decrypt. | `session.key` changed mid-flow (a deploy/rotation), or the cookie was tampered with. |
| `400 identity provider returned an error` | The IdP redirected back with an `error` query parameter. | The user denied consent, or the IdP rejected the request (misconfigured client at the IdP). |
| `400 invalid authentication response` | Missing `code`, or `state` doesn't match what HOG issued. | A stale/replayed callback URL, or a `state` mismatch (possible CSRF attempt, correctly rejected). |
| `502 authentication failed` | Code exchange or the userinfo fetch failed. | `clientSecret` is wrong, `redirectURL` doesn't exactly match what's registered at the IdP, or HOG can't reach the IdP's token/userinfo endpoint from where it runs. |
| `500 internal error` | Session issuance failed after a successful exchange. | Usually a `stateProvider` store write failure in delegated-state mode. |

If HOG **fails to start** rather than serving any of the above, `IdP.spec`
discovery against `issuer` failed — HOG performs OIDC discovery once at
boot and fails fast on a bad or unreachable issuer, rather than retrying
per request.

## Cookies not set (or not sent back)

- **Testing over plain HTTP.** The session/login cookies default to
  `Secure` unless the request's `X-Forwarded-Proto` header says `http`.
  If you're hitting HOG directly over `http://` in local development (no
  reverse proxy in front, no `X-Forwarded-Proto`), HOG still marks the
  cookie `Secure` — the browser will silently refuse to store or return it.
  `curl` won't show this problem (it doesn't enforce `Secure`), which makes
  it confusing. Fix: front local dev with TLS, or a proxy that sets
  `X-Forwarded-Proto: http` deliberately if you really want a non-TLS
  cookie for testing.
- **Cross-site frontend and API.** The cookie is `SameSite=Lax`. A `Lax`
  cookie is withheld on cross-site subrequests (`fetch`/`XHR`, not just
  top-level navigation) — if your SPA's origin and HOG's origin aren't
  the same site (differ by more than a subdomain), the browser won't send
  the cookie on API calls no matter how the request is made. This is a
  deliberate CSRF mitigation, not a bug — see
  [design: authentication and sessions](../design/auth-model.md#why-samesitelax-not-strict).
  Serve the frontend and the BFF from the same site.
- **A very large session.** HOG auto-chunks a session that doesn't fit one
  cookie across `<cookieName>.0`, `<cookieName>.1`, etc. If something
  downstream (a CDN, an old proxy) strips or reorders numbered cookies,
  the session will fail to reassemble. Keep `identity.claims` and
  `identity.groups` scoped to what you actually need projected.

See [security hardening](security.md) and
[configuration: `Gateway.session`](configuration.md#gateway-session) for
the underlying settings.
