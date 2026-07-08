# A BFF with OIDC login

This example configures HOG as a full Backend-for-Frontend: it runs the
OpenID Connect login flow on the browser's behalf, holds the session in an
encrypted, `HttpOnly` cookie, and reverse-proxies an authenticated route to a
backend — injecting the caller's identity as headers so the backend never has
to speak OIDC itself.

!!! note "Prerequisites"
    - An OIDC client registered with a provider that supports discovery
      (Keycloak, Auth0, Okta, Dex, …): an issuer URL, a client ID, a client
      secret, and a redirect URL.
    - A JSON backend reachable at the URL you put in `upstream` below.
    - Docker, and the `hog-runtime` image built locally as in the
      [quick start](quickstart.md) (`docker build -f build/Dockerfile.runtime -t hog-runtime .`).

## 1. Generate a session key

The session cookie is sealed with AES-256-GCM, which needs a key of exactly
32 bytes. `openssl rand -hex 16` prints 32 hex characters — a 32-byte string:

```sh
export SESSION_KEY=$(openssl rand -hex 16)
export OIDC_CLIENT_SECRET=your-client-secret
```

## 2. Write `gateway.yaml`

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  session:
    key: ${SESSION_KEY}
    ttl: 8h
---
kind: IdP
metadata: { name: default }
spec:
  type: oidc
  issuer: https://idp.example.com
  clientID: hog-example
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: http://localhost:8080/auth/callback
---
kind: Route
metadata: { name: app }
spec:
  match: /
  handler:
    type: static
    dir: /srv/web
  policy: { auth: required }
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  handler:
    type: reverse-proxy
    upstream: http://backend:9000
    stripPrefix: /api
    forwardAccessToken: true
  policy: { auth: required }
```

A few things worth noting:

- **`session.key`** must be exactly 32 bytes (AES-256); `session.ttl`
  defaults to `8h` if you omit it — it's shown here for clarity.
- **`kind: IdP`** is a standalone resource, not nested under `Gateway.spec`.
  HOG supports exactly one active `IdP` today. Its `redirectURL`'s path
  becomes the callback route automatically (`/auth/callback` here) — you
  don't declare a `Route` for it.
- The `/` route is `type: app` (inferred from `handler.type: static`) with
  `policy.auth: required`. Because `app` routes redirect rather than 401, an
  unauthenticated visit to `/` sends the browser straight into the login
  flow with **no frontend auth code** — no JS needed to detect "not logged
  in" and kick off login.
- The `/api/` route is `type: service` (inferred from `handler.type:
  reverse-proxy`), so `auth: required` here is actually the default for
  service routes — it's spelled out for clarity. `forwardAccessToken: true`
  is what puts the caller's access token on the outbound request to your
  backend; it's off by default.
- `/auth/login`, `/auth/logout`, and `/auth/session` are mounted
  automatically whenever both `session` and an `IdP` are configured — again,
  no `Route` resources needed.

## 3. Run it

Mount the config and your SPA build as read-only volumes — content and
config are always plain directories, never baked into a generic
`hog-runtime` container:

```sh
docker run --read-only --tmpfs /tmp \
  -p 8080:8080 \
  -e SESSION_KEY \
  -e OIDC_CLIENT_SECRET \
  -v "$(pwd)/gateway.yaml:/etc/hog/gateway.yaml:ro" \
  -v "$(pwd)/dist:/srv/web:ro" \
  hog-runtime
```

!!! success "Result"
    `"hog listening" addr=:8080` on stdout. HOG is now the OIDC relying
    party for this app.

## 4. Walk through the login flow

1. Open `http://localhost:8080/` in a browser. Because `/` requires auth and
   there's no session cookie yet, HOG responds `302` to
   `/auth/login?return_to=%2F`.
2. `/auth/login` seals `state`/`nonce` (and a PKCE verifier, on by default)
   into a short-lived `hog_login` cookie and redirects to your IdP's
   authorization endpoint.
3. You authenticate at the IdP and it redirects back to
   `http://localhost:8080/auth/callback?code=...&state=...`.
4. HOG verifies `state`, exchanges `code` for tokens, verifies the ID token
   against the IdP's JWKS, and — only if the identity model needs a claim
   the ID token doesn't carry — calls `userinfo`.
5. HOG seals the result into the `hog_session` cookie (`SameSite=Lax`,
   `HttpOnly`, `Secure` when behind TLS) and redirects to `return_to` (`/`).
6. Your SPA is served. Its own calls to `/api/...` now carry the session
   cookie, and HOG resolves it into the request's identity on every request.

## What the backend receives

For a request through `/api/...`, `stripPrefix: /api` removes the prefix
before proxying, and HOG rewrites the request before it reaches
`http://backend:9000`:

- **`X-User-Id`** — always present; the session subject. Never spoofable —
  HOG strips any inbound `X-User-*` header before injecting its own.
- **`X-User-Email`, `X-User-Name`, `X-User-Given-Name`, `X-User-Family-Name`**
  — one header per passport claim. The default claim set is `email`, `name`,
  `given_name`, `family_name`; configure a different set with `identity.claims`
  on the `Gateway` spec.
- **`X-User-Groups`** — only if `identity.groups` is configured (not in this
  example).
- **`Authorization: Bearer <access_token>`** — only because this route sets
  `forwardAccessToken: true`. Any client-supplied `Authorization` header is
  always removed first, so this is the only way a bearer token reaches the
  backend.
- **No `Cookie` header** — stripped by default (`forwardCookies: false`), so
  the backend never sees `hog_session` or `hog_login`.

## Next steps

- Gate this same route by group or a Rego rule:
  [Enforce authorization](authorization.md).
- Merge several backends behind one route:
  [Aggregate multiple backends](api-aggregation.md).
- Full session/identity/IdP field list: the
  [configuration reference](../operations/configuration.md) and
  [authentication](../operations/authentication.md).
