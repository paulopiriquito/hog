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

An instance that never logs anyone in — one that only verifies a Bearer
access token another party obtained — needs the `IdP` resource and nothing
else here. See
[an instance that only verifies tokens](#an-instance-that-only-verifies-tokens).

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
| `clientSecret` | string | — | **Required**, unless `verificationOnly`. Use `${ENV}` — never commit it. |
| `redirectURL` | string | — | **Required**, unless `verificationOnly`. Must match what the IdP is configured to redirect back to. Its **path** becomes HOG's callback endpoint (default `/auth/callback` if it can't be parsed). |
| `verificationOnly` | bool | `false` | Declares an IdP that only **verifies** access tokens someone else issued. It needs just `issuer` and `clientID`, runs no login flow, and **rejects** `clientSecret` and `redirectURL`. See [an instance that only verifies tokens](#an-instance-that-only-verifies-tokens). |
| `bearerAudience` | string | `clientID` | Expected `aud` for verifying `Authorization: Bearer` access tokens (see [API bearer auth](#api-clients-bearer-tokens) below). |
| `bearer.jwksURL` | string | discovery's `jwks_access_token_uri`, else `jwks_uri` | Key set used to verify `Authorization: Bearer` access tokens, for a provider that signs them with a key set other than the ID token's. |
| `bearer.audienceClaim` | string | — (checks `aud`) | Claim compared against `bearerAudience` instead of the standard `aud` (e.g. `client_id`). |
| `bearer.requireAudience` | bool | `true` | Set `false` to accept access tokens that carry no audience at all. Rejected at config load if combined with `bearer.audienceClaim` — the claim would then never be checked. |
| `bearer.signingAlgs` | []string | the provider's advertised ID-token algorithms | Signature algorithms accepted for access tokens. |
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

### Providers whose access tokens are not ID tokens

Some identity providers sign access tokens with a key set that isn't the ID
token's `jwks_uri` (advertised separately in discovery as
`jwks_access_token_uri`), carry the client identity in a claim such as
`client_id` rather than the standard `aud`, and omit `sub` entirely. The
`bearer:` block on the `IdP` resource, paired with `identity.subjectClaim`,
handles all three:

```yaml
kind: IdP
metadata: { name: corp-oidc }
spec:
  type: oidc
  issuer: https://idp.example.com
  clientID: ${OIDC_CLIENT_ID}
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: https://app.example.com/auth/callback
  bearer:
    audienceClaim: client_id
---
kind: Gateway
metadata: { name: my-gateway }
spec:
  identity:
    subjectClaim: uid   # the token's own subject-bearing claim, in place of sub
```

`bearer.jwksURL` overrides the access-token key set explicitly, for a
discovery document that doesn't advertise `jwks_access_token_uri`.
`bearer.requireAudience: false` accepts a token that carries no audience
claim at all; combining it with `bearer.audienceClaim` is rejected when the
config is loaded, since the claim would then never be checked.
`bearer.signingAlgs` restricts which signature algorithms an access token
may use, defaulting to whatever the discovery document advertises for ID
tokens. See the [configuration reference](configuration.md#gateway-identity)
for the full `identity.subjectClaim` field description.

### An instance that only verifies tokens

A deployment can split the two jobs across two HOG instances: one terminates
the OIDC session — it runs the authorization-code flow, holds the cookie and
refreshes the access token — while a second sits in front of the APIs and
only ever *verifies* a Bearer access token the first one obtained. The second
declares no `session`, so it mounts no login, logout or callback route and
never performs a code exchange.

Both still need an `IdP`: verifying a token means discovering the provider's
key set and checking the token's issuer. Only the first needs to be able to
log anyone in, and `verificationOnly: true` says so:

```yaml
kind: IdP
metadata: { name: corp-oidc }
spec:
  type: oidc
  verificationOnly: true          # verifies tokens; never starts a login flow
  issuer: https://idp.example.com # discovery finds the key set; every token is checked against it
  clientID: ${OIDC_CLIENT_ID}     # the expected audience
```

That is the whole resource. `clientSecret` and `redirectURL` are not merely
optional here — they are **rejected**, because a connector that never
exchanges an authorization code can never use the secret, and an instance
that mounts no callback route should not name one. So the verifying instance
needs no copy of the client secret in its secret store at all; see
[give each instance only the credentials it uses](security.md#give-each-instance-only-the-credentials-it-uses).

Everything on the verification side is unchanged: `bearerAudience`, the
`bearer:` block, `identity.subjectClaim`, group derivation, route
`access`/`authorize`, and both directions of the identity assertion below all
work exactly as they do on a full instance. Only the login flow is absent.

Two configurations are refused at startup rather than left half-working:

| Configuration | Why it fails |
|---|---|
| `verificationOnly: true` together with `clientSecret` or `redirectURL` | Neither is ever used, so accepting them would copy a credential into a workload that cannot spend it and name a route it does not serve. |
| `verificationOnly: true` on a gateway that also configures `session` | The login flow's callback is the only thing in HOG that issues a session cookie. The two together describe a login that can never happen: no login, callback or logout endpoint would be mounted, and every protected `app` route would redirect to a path that 404s. |

The declaration is always explicit. HOG never infers it from a missing
`session` block — adding a session to an existing config must not silently
change what its `IdP` resource means. An `IdP` that does not set the field
behaves exactly as before: all four of `issuer`, `clientID`, `clientSecret`
and `redirectURL` remain required.

### Handing identity to a second HOG instance

A front HOG instance — the one that terminated the browser session or
verified the Bearer token — can mint a signed statement of the principal it
resolved and attach it to a proxied request, so a second HOG instance behind
it can trust that identity without resolving it against the IdP a second
time — that second instance's own `IdP` is usually a verification-only one,
as above:

```yaml
# front instance: mints on the way out
spec:
  identity:
    assertion:
      issue:
        name: go-app                    # iss claim
        keyId: go-app-2026-09
        key: ${ASSERTION_SIGNING_KEY}   # base64, 32-byte Ed25519 seed
        ttl: 60s                        # default
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  handler:
    type: reverse-proxy
    upstream: http://api-instance:8080
    forwardIdentity: true   # only this hop carries the assertion
```

```yaml
# second instance: verifies on the way in
spec:
  identity:
    assertion:
      accept:
        issuer: go-app
        keys:
          - keyId: go-app-2026-09
            publicKey: ${ASSERTION_VERIFY_KEY}   # base64, the issuer's public key
```

The front instance mints a fresh assertion — an Ed25519-signed compact
JWS, never the caller's own access or session token — for every request on
a route with `forwardIdentity: true`, with a default 60-second lifetime.
The header (`X-Hog-Identity` by default) is stripped from every inbound
request on both instances before anything reads it, so a client can never
supply one of its own; it is set only by HOG itself, on the outbound hop.
The accepting instance verifies the signature and issuer and, by default
(`requireBearer: true`), uses the assertion only to *enrich* the principal
its own Bearer check already authenticated — a header alone does not
authenticate a request unless you explicitly set `requireBearer: false`.

The assertion carries no audience and no unique id: any instance configured
with the matching issuer and key accepts it, and a captured assertion can be
replayed for the rest of its lifetime plus the 30-second clock-skew
allowance every check applies — which is exactly what the `ttl` (60s by
default) bounds. Accepting one is trusting the issuing instance's own
resolution of the passport and groups it asserts, not independently
re-deriving them: it is a trust relationship between two deployments you
run, scoped by `issuer` and `keys`, not a substitute for authenticating the
caller at the edge. See [design: authentication and
sessions](../design/auth-model.md#the-identity-assertion) for the rationale.

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
