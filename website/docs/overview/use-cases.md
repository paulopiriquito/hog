# Use cases

HOG covers four recurring jobs in a frontend/backend stack. You can use one
of them or all four in the same deployment — they're all just routes.

## SPA host

**The problem.** You have a built single-page application (React, Vue,
Svelte, or similar) and need something to serve its static files, fall back
to `index.html` for client-side routes, and resist path traversal — without
standing up nginx just for that.

**How HOG solves it.** A `static` terminal serves your build output through
`os.Root`, which confines file access to the configured directory regardless
of what the request path contains. SPA fallback is on by default:

```yaml
kind: Route
metadata: { name: spa }
spec:
  match: /
  handler: { type: static, dir: /srv/web }
  policy: { auth: public }
```

This is the exact shape the `hog-static` base image ships with. See the
[quick start](../examples/quickstart.md) to run it in minutes.

## BFF for a SPA

**The problem.** Your SPA needs a backend-for-frontend (BFF) — a server-side
component that logs users in through your identity provider and calls your
backend APIs as that user — without shipping OpenID Connect (OIDC) client
code, access tokens, or secrets to the browser.

**How HOG solves it.** HOG terminates an OIDC Authorization Code flow with
PKCE (Proof Key for Code Exchange) at built-in `/auth/login` and
`/auth/logout` endpoints. The resulting session lives in an encrypted,
`HttpOnly` cookie the browser never reads. Downstream requests pass through
the projection stage, which strips
any inbound `X-User-*` headers (anti-spoofing) and injects the resolved
identity as headers your backend can trust; a `reverse-proxy` or `api`
terminal can additionally forward the access token as a bearer credential
(`forwardAccessToken: true`) without the frontend ever touching it. The
refresh token — the longest-lived credential — never reaches the browser at
all, not even inside the encrypted cookie. See
[architecture](../architecture/index.md) for the full request lifecycle and
[configuration reference](../operations/configuration.md) for setting up an
IdP.

## API gateway / aggregator

**The problem.** Your frontend needs data from one or several backend
services, but you don't want the browser making cross-origin calls, handling
CORS, or knowing internal service addresses.

**How HOG solves it.** A `reverse-proxy` terminal transparently forwards to
a single backend — streaming responses (server-sent events, WebSockets)
included. An `api` terminal fans a request out to 1..N backends concurrently
and merges the results into one JSON object keyed by backend name:

```yaml
spec:
  match: /api/dashboard
  handler:
    type: api
    backends:
      - { group: profile, upstream: ${PROFILE_SVC}, path: /me }
      - { group: orders, upstream: ${ORDERS_SVC}, path: /list }
```

A response plugin can reshape that merged payload before it reaches the
client. See [configuration reference](../operations/configuration.md) for
backend options, and the [developer guide](../developer/index.md) to write a
reshaping plugin.

## Authorization enforcement point

**The problem.** Not every authenticated user should reach every route, and
group membership alone isn't always enough — you sometimes need conditional
logic over claims and request attributes.

**How HOG solves it.** `Policy` resources declare a `require` block (group
membership and/or claim values) and, for anything more expressive, an
embedded Open Policy Agent engine evaluating a Rego file — no sidecar, no
network hop:

```yaml
kind: Policy
metadata: { name: admins-only }
spec:
  require: { groups: [admins] }
```

Attach policies to a route or route group by name; the authz stage evaluates
every matching policy and returns `403` on the first deny. A Rego evaluation
error is treated as a deny, not an allow — HOG fails closed. See
[architecture](../architecture/index.md) for where authorization sits in the
chain and [configuration reference](../operations/configuration.md) for the
full `Policy` schema.
