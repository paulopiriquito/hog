# Configure authentication

HOG is the OpenID Connect *relying party* for your frontend: it runs the
whole login protocol and hands your single-page app a session, never a
token. This page walks through wiring it up. For the design rationale, see
[design: authentication and sessions](../design/auth-model.md).

Three pieces work together, and **all three must be present** for the login
endpoints to be mounted:

1. A `kind: IdP` resource — the OIDC connector.
2. `Gateway.spec.session` — the encrypted session cookie.
3. Routes marked `access: { auth: required }` (directly or via a
   `RouteGroup`) to protect.

If `session` is configured without an `IdP`, HOG logs a startup warning and
serves protected routes' redirect behavior without the login/callback/logout
endpoints actually existing — fix this by adding the `IdP` resource.

## 1. Configure the IdP connector

```yaml
kind: IdP
metadata: { name: corp-oidc }
spec:
  type: oidc
  issuer: https://idp.example.com
  clientID: ${OIDC_CLIENT_ID}
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: https://app.example.com/auth/callback
  scopes: [openid, profile, email]
  pkce: true
```

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | `oidc` (the only built-in connector). |
| `issuer` | string | — | **Required.** OIDC discovery issuer URL. HOG performs discovery at startup — a bad issuer fails the boot fast. |
| `clientID` | string | — | **Required.** |
| `clientSecret` | string | — | **Required.** Use `${ENV}` — never commit it. |
| `redirectURL` | string | — | **Required.** Must match what the IdP is configured to redirect back to. Its **path** becomes HOG's callback endpoint (default `/auth/callback` if it can't be parsed). |
| `bearerAudience` | string | `clientID` | Expected `aud` for verifying `Authorization: Bearer` access tokens (see [API bearer auth](#api-clients-bearer-tokens) below). |
| `scopes` | []string | `[openid, profile, email]` | Requested OAuth scopes. |
| `pkce` | bool | `true` | Use PKCE (`S256`) on the authorization code flow. |

Only one `IdP` resource is supported per config today.

## 2. Configure the session

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  session:
    key: ${SESSION_KEY}
    ttl: 8h
```

`session.key` must be **exactly 32 bytes** — it's used directly as an
AES-256-GCM key that seals the session cookie. Generate one and keep it out
of the repo:

```sh
openssl rand -base64 24 | head -c 32   # trims to exactly 32 bytes
```

!!! warning "The session key is a secret"
    Anyone with the key can forge or decrypt sessions. Always supply it via
    `${ENV}` (backed by a secret store — Kubernetes `Secret`, Vault, your
    platform's equivalent), never as a literal in a checked-in config file.
    Rotating the key invalidates every existing session.

The cookie is `SameSite=Lax`, `HttpOnly`, and `Secure` (unless the inbound
request's `X-Forwarded-Proto` says `http`, which only matters for local,
non-TLS testing — see [security hardening](security.md)). `Lax`, not
`Strict`, is deliberate: the browser's return trip from the IdP's login page
to `/auth/callback` is a top-level cross-site navigation, and a `Strict`
cookie would be withheld on exactly that request, breaking login.

If the sealed session is larger than one cookie can hold, HOG transparently
chunks it across `<cookieName>.0`, `<cookieName>.1`, etc.

### The identity model

`identity` controls which claims land in the session (and, projected, in
`X-User-*` headers) and how group membership is derived:

```yaml
spec:
  identity:
    claims: [email, name]
    groups:
      source: isMemberOf       # userinfo/token claim holding a DN array
      match: ["ou=engineering"] # keep DNs containing any of these (case-insensitive)
      render: cn                # or "dn" for the whole DN
      as: groups
    userInfo: auto               # auto | always | never
```

`groups.match` must list at least one pattern — an empty list matches
nothing, silently producing an empty group set. See the
[configuration reference](configuration.md#gateway-identity) for full field
defaults.

## 3. Protect routes

Auth defaults from a route's `type`: `service` routes (`reverse-proxy`,
`api`) require auth by default; `app` routes (`static`) are public by
default. Set `access.auth` explicitly to override, either per-route or on a
`RouteGroup`:

```yaml
kind: Route
metadata: { name: dashboard, labels: { tier: app } }
spec:
  match: /app/
  handler: { type: static, dir: /srv/web }
  access: { auth: required }
```

An unauthenticated request to a required **app** route gets a `302` redirect
to `loginPath` with `?return_to=<original URI>` (validated to be a local
path — no open redirect). A required **service** route instead returns
`401` with a `WWW-Authenticate` header.

## The endpoints

Mounted only when both `session` and an `IdP` are configured, at the paths
from `Gateway.spec.auth` (defaults shown):

| Path | Default | Behavior |
|---|---|---|
| Login | `/auth/login` | Starts the OIDC Authorization Code flow (with PKCE if enabled); redirects to the IdP. |
| Callback | derived from `IdP.spec.redirectURL`, else `/auth/callback` | Exchanges the code, verifies the ID token, fetches userinfo if the identity model needs it, and issues the session cookie. |
| Logout | `/auth/logout` | **`POST` only** (a non-`POST` returns `405`). Clears the HOG session and redirects to `session.postLogoutRedirect` (default `/`). Same-origin: because it is an unsafe method, the gateway-wide [security stage](security.md) rejects a cross-origin logout — so trigger it with a same-origin `POST` (a form or `fetch('/auth/logout', { method: 'POST' })`), not a link. This is a HOG-only logout — it does not end the IdP's own session. |
| Session info | `session.infoPath`, default `/auth/session` | `GET` → `200` with the public session view (subject, passport, groups, expiry — never tokens) if a valid session is present, else `401`. This is what your SPA polls to know who's logged in. |

```yaml
spec:
  auth:
    loginPath: /auth/login
    logoutPath: /auth/logout
```

## API clients: Bearer tokens

Routes typed `service` also accept `Authorization: Bearer <access-token>`
from non-browser clients. HOG verifies the JWT offline against the IdP's
JWKS (signature, issuer, expiry, and `aud` — `bearerAudience` or the client
ID by default) and projects it into the same identity shape the cookie flow
produces. Cookie resolution always takes priority: if a valid session
cookie is present, the Bearer header is ignored. Bearer is never evaluated
on `app` routes.

## A complete worked example

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
  session:
    key: ${SESSION_KEY}
    ttl: 8h
  identity:
    claims: [email, name]
---
kind: IdP
metadata: { name: corp-oidc }
spec:
  type: oidc
  issuer: https://idp.example.com
  clientID: ${OIDC_CLIENT_ID}
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: https://app.example.com/auth/callback
---
kind: Route
metadata: { name: spa, labels: { tier: app } }
spec:
  match: /
  handler: { type: static, dir: /srv/web }
  access: { auth: required }
---
kind: Route
metadata: { name: api, labels: { tier: service } }
spec:
  match: /api/
  type: service
  handler:
    type: reverse-proxy
    upstream: http://backend:9000
    stripPrefix: /api
  access: { auth: required }
```

See [troubleshooting](troubleshooting.md) for common login/session failures,
and [scaling](scaling.md#session-state) for running silent token refresh
across multiple replicas.
