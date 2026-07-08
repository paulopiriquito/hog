# Security hardening

A checklist for running HOG in production. Each item is grounded in real
config or behavior — follow the links for the details.

## Run the container non-root and read-only

`hog-runtime`/`hog-static` already run as the non-root `hog` user
(uid/gid `10001`) by default. HOG is stateless, so also run the root
filesystem read-only:

```sh
docker run --read-only --tmpfs /tmp -p 8080:8080 hog-static
```

Mount `/etc/hog` (config) and `/srv/web` (content) read-only if you aren't
baking them into the image. See
[installation](installation.md#running-read-only-as-the-non-root-user).

## Keep the session key a secret

`session.key` is an AES-256-GCM key: anyone who has it can forge or decrypt
sessions. Always inject it with `${ENV}` from a secret store, never as a
literal in a config file you commit:

```yaml
spec:
  session:
    key: ${SESSION_KEY}
```

Rotating the key invalidates every existing session (a deliberate
trade-off — see [authentication](authentication.md#2-configure-the-session)).

## Terminate TLS at a trusted load balancer, and set `trustedProxies`

HOG does not terminate TLS itself. Deploy it behind a TLS-terminating load
balancer or ingress, and tell HOG which peer to trust with
`Gateway.spec.trustedProxies`: a gateway-wide `forwarded` layer strips
`X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host`,
`X-Forwarded-Port`, `X-Real-Ip`, and `Forwarded` from any request whose
immediate peer isn't listed there, before routing and before OpenTelemetry
ever see the request. `trustedProxies` takes CIDRs or bare IPs; `"*"` trusts
every peer (only appropriate if HOG's listener is otherwise unreachable
except through your proxy); the **default — an empty list — trusts no
peer**, so those headers are stripped from every request until you configure
it. This is the secure default, but it also means `X-Forwarded-Proto` (used
to decide whether cookies are marked `Secure`), the client IP HOG logs and
projects, and the `X-Forwarded-*` chain forwarded to backends all silently
fall back to "no proxy" values (always-`Secure` cookies, the immediate peer
as client IP) until you set this field:

```yaml
spec:
  trustedProxies: ["10.0.0.0/8"]   # your LB's/ingress's CIDR
```

Network-level isolation (a private subnet, a service mesh, security groups)
is still what stops an untrusted party from reaching HOG directly in the
first place — `trustedProxies` decides which of the peers that *can* reach
HOG are allowed to set forwarded headers, it doesn't substitute for network
isolation. See the [configuration reference](configuration.md#gateway) note
on this field.

## Leave `SameSite=Lax` alone

The session, login-state, and chunked cookies are all set `SameSite=Lax`,
`HttpOnly`, and `Secure` (when the request is HTTPS). This isn't
configurable, and that's intentional: `Strict` breaks the OIDC redirect back
to `/auth/callback`, and `Lax` still blocks the cross-site subrequests and
form submissions that matter for CSRF. See
[design: authentication and sessions](../design/auth-model.md#why-samesitelax-not-strict).

## Don't forward credentials to backends unless you need to

Both `reverse-proxy` and `api` backends default to **not** forwarding the
caller's cookies, and **not** injecting the session's access token:

```yaml
handler:
  type: reverse-proxy
  upstream: http://backend:9000
  forwardCookies: false       # default — HOG's own cookies never leak downstream
  forwardAccessToken: false   # default — enable only if the backend needs to call further upstream as the user
```

`forwardCookies: true` and `forwardAccessToken: true` are real, supported
options — only turn them on for backends that specifically need the raw
cookie or the ability to act as the user. The client-supplied `Authorization`
header is always stripped before HOG decides whether to inject its own,
regardless of these flags, so a client can never smuggle a bearer token
straight through to a backend.

## Authorization is fail-closed

- A route with an empty `access.authorize` skips the authorization gate
  (default-allow — authorization is opt-in per route, not implicit).
- A route with `access.authorize` names denies on **any** policy denying
  (deny-overrides), on an unsatisfied `require`, and on a Rego evaluation
  error (never silently allows on error).
- Denied requests get a generic `403 forbidden` with no policy detail in
  the body — the reason is logged and recorded on the trace span, not
  returned to the client.

See [authorization](authorization.md#how-a-decision-is-reached).

## No credentials in logs or traces

- Access tokens and refresh tokens are never logged, never placed in span
  attributes, and never included in the `input` passed to authorization
  policies.
- The access log redacts sensitive query parameters (`code`, `state`,
  `token`, `access_token`, `id_token`, `refresh_token`, `api_key`,
  `client_secret`, `password`, `assertion`) and refuses to capture
  `Authorization`, `Cookie`, `Proxy-Authorization`, or `Set-Cookie` via
  `accessLog.headers` (a startup error if you try).

See [observability](observability.md#credentials-are-never-logged-or-traced).

## Only allow `insecureSkipVerify` for a reason you can justify

`reverse-proxy.insecureSkipVerify: true` disables upstream TLS certificate
verification. It exists for internal backends with self-signed certs you
already trust by network position — don't set it for anything reachable
outside your own infrastructure.

## CSRF protection and security headers are on by default

`Gateway.spec.security` is applied gateway-wide, wrapping the whole handler
just inside the `forwarded` layer and just outside OpenTelemetry/routing —
it covers every route and the raw `/auth/*` endpoints alike, not just some
routes.

- **CSRF (`security.csrf`, on by default).** Built on
  `net/http.CrossOriginProtection`: a token-less, Fetch-metadata-based
  defense that allows `GET`/`HEAD`/`OPTIONS`, same-origin requests, and
  non-browser requests (no `Sec-Fetch-Site`/`Origin` header — so
  `Authorization: Bearer` API clients are entirely unaffected), and rejects a
  cross-origin, state-changing browser request with `403`. This is
  **defense-in-depth on top of `SameSite=Lax`** (above), not a replacement
  for it — `SameSite=Lax` is still what keeps the session cookie itself off
  cross-site requests in the first place.
- **Security response headers (`security.headers`, on by default).**
  `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin`, and
  `Strict-Transport-Security` (1 year, `includeSubDomains`) are set on every
  response unless you override or blank them out. `Content-Security-Policy`
  is opt-in (unset by default) — HOG can't pick a safe default CSP without
  knowing your frontend's script/style/asset origins.

See the [configuration reference](configuration.md#gateway-security) for the
full field table and defaults.

!!! warning "A same-site, cross-origin SPA needs `csrf.trustedOrigins`"
    If your frontend and HOG share a registrable domain but live on different
    subdomains — e.g. `app.example.com` calling a HOG instance at
    `api.example.com` — the browser sends `Sec-Fetch-Site: same-site`, which
    `CrossOriginProtection` still treats as cross-origin. A state-changing
    request (`POST`/`PUT`/`PATCH`/`DELETE`) from that frontend gets `403`
    until you add `https://app.example.com` to `security.csrf.trustedOrigins`.

!!! note "`bypassPatterns` is a deliberate CSRF hole"
    `security.csrf.bypassPatterns` exempts specific `ServeMux`-style patterns
    from CSRF checking entirely — for an endpoint that legitimately can't
    send `Origin`/`Sec-Fetch-Site` (e.g. a third-party webhook receiver).
    Scope each pattern as narrowly as possible; anything it matches accepts
    cross-origin state-changing requests with no CSRF defense at all.
