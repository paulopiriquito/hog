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

## Terminate TLS at a trusted load balancer

HOG does not terminate TLS itself. Deploy it behind a TLS-terminating load
balancer or ingress, and make sure only that proxy can reach HOG directly —
`X-Forwarded-Proto` (used to decide whether cookies are marked `Secure`) and
`X-Forwarded-For` are currently trusted from **any** caller that can reach
HOG, since the `Gateway.spec.trustedProxies` field is parsed but not yet
enforced against the request path. Network-level isolation (a private
subnet, a service mesh, security groups) is what actually protects you
today — don't rely on `trustedProxies` alone. See the
[configuration reference](configuration.md#gateway) note on this field.

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

- A route with no `policies` skips the authorization gate (default-allow —
  authorization is opt-in per route, not implicit).
- A route with policies denies on **any** policy denying (deny-overrides),
  on an unsatisfied `require`, and on a Rego evaluation error (never
  silently allows on error).
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

!!! note "The CSRF/security-headers middleware slot is reserved, not yet implemented"
    HOG's fixed middleware chain has a named `security` slot between the
    access log and session resolution, intended for CSRF defenses and
    security response headers. In the current codebase that slot is a
    pass-through placeholder — no security headers or CSRF checks are
    applied by HOG itself yet. Until it ships, apply security headers
    (`Content-Security-Policy`, `X-Frame-Options`, `Strict-Transport-Security`,
    etc.) and any additional CSRF protection at your load balancer/CDN, and
    rely on `SameSite=Lax` as described above in the meantime.
