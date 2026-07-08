# Configuration reference

HOG is configured with Kubernetes-style YAML **resources**. This page is the
authoritative field-by-field reference for every resource `kind`. For the
concepts behind resources, routes, and the middleware chain, see
[core concepts](../overview/concepts.md).

## Loading model

`--config` accepts a single file or a directory:

- A directory is read in lexical filename order over its `*.yaml`/`*.yml`
  files. That order becomes the **document order**, which decides plugin and
  policy layering when several resources apply to the same route.
- A file (or each file in a directory) may contain multiple `---`-separated
  YAML documents.
- Every resource has the same envelope:

```yaml
apiVersion: v1     # accepted but not currently validated
kind: Gateway
metadata:
  name: my-gateway
  labels: {}       # optional; used by RouteGroup/plugin selectors
spec:
  # kind-specific fields — documented below
```

- Every string value supports `${ENV}` interpolation, resolved once at
  startup against the process environment:
    - `${VAR}` — required; startup fails if `VAR` is unset.
    - `${VAR:-default}` — falls back to `default` if `VAR` is unset. An
      empty-but-set `VAR` is used as-is (the default does not apply).

```yaml
spec:
  session:
    key: ${SESSION_KEY}                # fails fast if unset
  otlp:
    endpoint: ${OTLP_ENDPOINT:-http://localhost:4318}
```

A config must contain **exactly one** `Gateway` resource and **at most one**
`Telemetry` resource; any number of `Route`, `RouteGroup`, `Policy`,
`RequestPlugin`, and `ResponsePlugin` resources; and **at most one** `IdP`
resource (multi-IdP is not yet supported).

---

## Gateway

The root resource. `listen` is the only field with a default; everything
else is opt-in.

| Field | Type | Default | Description |
|---|---|---|---|
| `listen` | string | `:8080` | The address `net/http` listens on. |
| `trustedProxies` | []string | — | CIDR/IP list intended to scope trust of `X-Forwarded-*` headers to your ingress. Parsed and validated but **not yet enforced** at request time — see the note below. |
| `plugins` | []string | — | Build-time module manifest for `hog-build`: `<import-path>[@version]` entries. Consumed by the build tool, not at runtime. See [installation](installation.md) and [building a custom binary](../developer/building-binaries.md). |
| `session` | mapping | — | The session/cookie block. See [Gateway: session](#gateway-session) and [authentication](authentication.md). |
| `identity` | mapping | — | The shared identity/passport model, used by both cookie and Bearer auth. See [Gateway: identity](#gateway-identity). |
| `auth` | mapping | — | Login/logout endpoint paths. See [Gateway: auth](#gateway-auth). |
| `stateProvider` | mapping | — | Server-side session state backend (opt-in). See [Gateway: stateProvider](#gateway-stateprovider) and [scaling](scaling.md). |

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
```

!!! note "`trustedProxies` is reserved"
    The field decodes and is available on `Gateway.Settings`, but at the time
    of writing no code path in the request pipeline consults it —
    `X-Forwarded-Proto`/`X-Forwarded-For` are currently trusted unconditionally
    wherever HOG reads them (e.g. to decide whether to mark cookies `Secure`).
    Terminate TLS at a proxy that only accepts traffic from your ingress; see
    [security hardening](security.md).

### Gateway: `session`

Configures the encrypted session cookie. Omit the block entirely to run
without sessions (a pure reverse-proxy/aggregation gateway). See
[authentication](authentication.md) for the full picture, including how this
interacts with `identity`, `auth`, and the `IdP` resource.

| Field | Type | Default | Description |
|---|---|---|---|
| `key` | string | — | **Required** if `session` is present. Must be exactly 32 bytes (used as an AES-256-GCM key). Use `${ENV}` — never commit it. |
| `cookieName` | string | `hog_session` | The session cookie's name (chunked as `<name>.0`, `<name>.1`, … if the sealed value exceeds one cookie). |
| `ttl` | duration string | `8h` | Session lifetime, e.g. `"8h"`, `"30m"`. |
| `fingerprintHeaders` | []string | `["User-Agent"]` | Request headers hashed into a server-side fingerprint, checked on every read; a mismatch invalidates the session. |
| `infoPath` | string | `/auth/session` | Path of the SPA-facing session-info endpoint (`GET` → the public session view; only mounted when both `session` and an `IdP` are configured). |
| `postLogoutRedirect` | string | `/` | Where `logoutPath` redirects to after clearing the session. |

```yaml
spec:
  session:
    key: ${SESSION_KEY}
    ttl: 8h
    fingerprintHeaders: ["User-Agent"]
```

### Gateway: `identity`

The claim/group projection model shared by the cookie session and Bearer
auth. Omitting the block uses the defaults below.

| Field | Type | Default | Description |
|---|---|---|---|
| `claims` | []string | `["email", "name", "given_name", "family_name"]` | Allowlist of ID-token/userinfo claims persisted into the passport (`sub` is always kept separately). |
| `groups` | mapping | — | Optional group-DN projection (see below). |
| `groups.source` | string | — | The userinfo/token claim holding the group-DN array (e.g. `isMemberOf`). |
| `groups.match` | []string | — | Case-insensitive substring patterns; only DNs containing at least one are kept. **A group with no `match` entries never matches anything** — set at least one pattern for `groups` to have any effect. |
| `groups.render` | string | `cn` | `cn` (extract the `cn=` component) or `dn` (keep the whole DN). |
| `groups.as` | string | `groups` | The session field / default projected-header name for the rendered group list. |
| `userInfo` | string | `auto` | `auto` (fetch userinfo only if the token is missing a configured claim/group source), `always`, or `never`. |

```yaml
spec:
  identity:
    claims: [email, name]
    groups:
      source: isMemberOf
      match: ["ou=engineering"]
      render: cn
      as: groups
```

### Gateway: `auth`

Endpoint paths for the browser login flow. Only meaningful (and only mounted)
when both `session` and an `IdP` resource are configured.

| Field | Type | Default | Description |
|---|---|---|---|
| `loginPath` | string | `/auth/login` | Starts the OIDC flow; redirects to the IdP. |
| `logoutPath` | string | `/auth/logout` | Clears the HOG session and redirects to `session.postLogoutRedirect`. |

The callback path is **not** set here — it's derived from the `IdP`
resource's `redirectURL` path (default `/auth/callback` if none is
configured or it can't be parsed). See [authentication](authentication.md).

```yaml
spec:
  auth:
    loginPath: /auth/login
    logoutPath: /auth/logout
```

### Gateway: `stateProvider`

Opt-in server-side session storage, for silent access-token refresh across a
multi-instance deployment. Requires `session` to also be configured (the
session key encrypts the at-rest record). See
[scaling](scaling.md#session-state) for when you need this.

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | **Required.** The registered `StateProvider` module name (a plugin you write — HOG ships none built in). |
| `refreshSkew` | duration string | `60s` | How early before the access token's expiry to silently refresh it. |
| `keyPrefix` | string | `hog:sess:` | Prefix applied to the store key derived from the session ID. |
| `config` | mapping | — | Opaque; passed verbatim to the registered `StateProvider` factory (e.g. Redis connection settings). |

```yaml
spec:
  session:
    key: ${SESSION_KEY}
  stateProvider:
    type: redis
    refreshSkew: 60s
    config:
      addr: redis:6379
```

---

## Route

A single routable endpoint. `match` and `handler.type` are required.

| Field | Type | Default | Description |
|---|---|---|---|
| `match` | string | — | **Required.** A [`net/http.ServeMux`](https://pkg.go.dev/net/http#ServeMux) pattern, e.g. `/api/users/{id}`, `GET /health`, `/app/` (trailing slash = subtree). Must be unique across all routes. |
| `type` | string | inferred | `app` or `service`. If unset, inferred from `handler.type`: `reverse-proxy`/`api` → `service`; everything else (`static`, …) → `app`. Determines the default `auth` and how the auth gate responds (redirect vs. `401`). |
| `handler` | mapping | — | **Required.** `{ type: <name>, ...handler-specific fields }` — see [Handler types](#handler-types). |
| `policy` | mapping | — | This route's own `auth`/`projection` (see [Policy](#policy-route-auth-projection) below). Always wins over a matching `RouteGroup`. |
| `policies` | []string | — | Names of `kind: Policy` (authorization) resources to enforce on this route, unioned with any matching `RouteGroup`'s `policies`. See [authorization](authorization.md). |

```yaml
kind: Route
metadata: { name: dashboard, labels: { tier: api } }
spec:
  match: /api/dashboard
  type: service
  handler:
    type: api
    backends: [...]
  policy: { auth: required }
  policies: [admins-only]
```

### Policy (route `auth` + projection)

Not to be confused with the `kind: Policy` authorization resource — this is
the inline `policy:` block on a `Route`/`RouteGroup`.

| Field | Type | Default | Description |
|---|---|---|---|
| `auth` | string | inferred | `required` or `public`. Unset defaults from the route's `type`: `service` → `required`, `app` → `public`. |
| `projection` | mapping | derive from passport | Customizes the `X-User-*` headers injected for the backend. See below. |

`projection`:

| Field | Type | Description |
|---|---|---|
| `session.claims` | map[string]string | Explicit `claim → header` overrides. When set, **only** these claims are projected (replaces the default "one `X-User-<Claim>` per passport claim" behavior). |
| `session.groups.header` | string | Overrides the groups header name (default derives from `identity.groups.as`, e.g. `X-User-Groups`). |
| `request` | mapping | Reserved; decodes but has no effect yet. |

```yaml
spec:
  policy:
    auth: required
    projection:
      session:
        claims:
          email: X-User-Email
        groups:
          header: X-User-Roles
```

---

## RouteGroup

Applies a shared `policy` to every `Route` whose labels match `selector` —
the same pattern as a Kubernetes Service selecting Pods. A route's own
`spec.policy`/`spec.policies` always wins over a matching group; when
several groups match, later groups (document order) override earlier ones
field-by-field.

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | Default route `type` (`app`/`service`) for matching routes that don't set their own. |
| `selector` | mapping | matches everything | `matchLabels` (exact-match map) and/or `matchExpressions` (see below). |
| `policy` | mapping | — | Same shape as [Route's `policy`](#policy-route-auth-projection). |
| `policies` | []string | — | Authorization policy names, unioned into every matching route's effective set. |

`selector.matchExpressions[]`:

| Field | Type | Description |
|---|---|---|
| `key` | string | The label to test. |
| `operator` | string | `In`, `NotIn`, `Exists`, or `DoesNotExist`. |
| `values` | []string | Comparison set for `In`/`NotIn`. |

```yaml
kind: RouteGroup
metadata: { name: app-auth }
spec:
  selector:
    matchLabels: { tier: api }
  policy: { auth: required }
  policies: [require-admins]
```

---

## Policy (authorization)

A named, reusable authorization unit referenced from `Route`/`RouteGroup`
`policies:`. See [authorization](authorization.md) for how policies combine
and evaluate; this table is the field reference.

| Field | Type | Default | Description |
|---|---|---|---|
| `require` | mapping | — | Built-in group/claim rule. At least one of `require`/`rego` is required; an empty (no `groups`/`claims`) `require` block is a config error. |
| `require.groups` | []string | — | Any-of: the principal must belong to at least one listed group. |
| `require.claims` | map[string]string\|[]string | — | All-of across keys; each value is a scalar or list (any-of within that claim). An empty value list is a config error. |
| `rego` | mapping | — | Embedded OPA/Rego rule. |
| `rego.path` | string | — | Path (file or directory) to `.rego` source, resolved relative to where the config is loaded. Must define `deny` under `package hog.authz`. |

```yaml
kind: Policy
metadata: { name: admins-only }
spec:
  require:
    groups: [admins]
    claims:
      tier: [gold, platinum]
```

---

## Handler types

Set via `handler.type`; the remaining fields under `handler` are specific to
that type. Built-in types: `static`, `reverse-proxy`, `api`, `health`.

### `static`

Traversal-safe file serving (built on `os.Root`) with single-page-app (SPA)
fallback.

| Field | Type | Default | Description |
|---|---|---|---|
| `dir` | string | — | **Required.** Directory to serve. |
| `index` | string | `index.html` | Filename served for the directory root and, on a miss, the SPA fallback shell. |
| `spaFallback` | bool | `true` | On a miss for an extensionless path, serve `index` instead of `404`. |
| `stripPrefix` | string | — | Prefix trimmed from the request path before resolving a file. |
| `cacheControl` | string | — | `Cache-Control` value applied to non-index files. The index file is always served `Cache-Control: no-cache`. |

Dotfiles and path-traversal segments (anything starting with `.`, including
`..`) are always rejected, independent of `spaFallback`.

```yaml
spec:
  match: /
  handler:
    type: static
    dir: /srv/web
  policy: { auth: public }
```

### `reverse-proxy`

A transparent, single-backend proxy (`net/http/httputil.ReverseProxy`).
Streams responses (SSE, WebSockets).

| Field | Type | Default | Description |
|---|---|---|---|
| `upstream` | string | — | **Required.** Base URL, e.g. `http://users-svc:9000`. |
| `stripPrefix` | string | — | Prefix trimmed from the outbound path. |
| `preserveHost` | bool | `false` | Forward the inbound `Host` header instead of the upstream's. |
| `forwardAccessToken` | bool | `false` | Inject `Authorization: Bearer <access token>` from the session principal. Off by default — see [security hardening](security.md). |
| `forwardCookies` | bool | `false` | Pass the inbound `Cookie` header through. Off by default — HOG's own session/login cookies are never meant to reach a backend. |
| `timeout` | duration string | none | Per-request timeout. A timed-out request returns `504`; any other proxy error returns `502`. |
| `insecureSkipVerify` | bool | `false` | Disable upstream TLS certificate verification. Use only for trusted internal backends with self-signed certs. |

```yaml
spec:
  match: /users/
  type: service
  handler:
    type: reverse-proxy
    upstream: http://users-svc:9000
    stripPrefix: /users
    timeout: 10s
```

### `api`

Fans out to 1..N backends concurrently and merges their JSON responses under
a key per backend.

| Field | Type | Default | Description |
|---|---|---|---|
| `timeout` | duration string | none | Overall request timeout, applied to all backend calls via a shared context. |
| `backends` | []mapping | — | **Required, at least one.** See below. |

`backends[]`:

| Field | Type | Default | Description |
|---|---|---|---|
| `group` | string | — | **Required, unique per handler.** The JSON key the backend's response is merged under. |
| `upstream` | string | — | **Required.** Base URL. |
| `path` | string | — | **Required.** Request path, joined onto the upstream. Supports `{name}` placeholders resolved from the route's own path parameters (e.g. a route matched as `/api/orders/{id}` can use `path: /orders/{id}`). |
| `method` | string | `GET` | HTTP method for the backend call. |
| `required` | bool | `true` | If `true`, a failed/timed-out call fails the whole request (`502`/`504`); if `false`, the backend is omitted and its group name is listed in the `X-Hog-Partial` response header. |
| `forwardQuery` | bool | `false` | Forward the inbound request's query string to this backend. |
| `forwardAccessToken` | bool | `false` | Inject `Authorization: Bearer <access token>` for this backend only. |

A 2xx response that isn't valid JSON (including an empty body) is treated as
a backend failure. A single backend response is capped at 10 MiB.

```yaml
spec:
  match: /api/dashboard
  type: service
  handler:
    type: api
    timeout: 5s
    backends:
      - group: profile
        upstream: http://users-svc:9000
        path: /me
      - group: notifications
        upstream: http://notif-svc:9100
        path: /unread
        required: false
```

### `health`

A built-in liveness/readiness handler. No config fields — it always returns
`200 {"status":"ok"}`. Mount it on whatever path and labels you want:

```yaml
spec:
  match: /healthz
  handler: { type: health }
  policy: { auth: public }
```

---

## Other resource kinds

- `kind: IdP` — the OIDC connector. See [authentication](authentication.md).
- `kind: Telemetry` — OpenTelemetry + access-log settings. See
  [observability](observability.md).
- `kind: RequestPlugin` / `kind: ResponsePlugin` — third-party middleware
  slots, selected onto routes the same way a `RouteGroup` is (`selector` +
  `config`). See [developer: writing plugins](../developer/writing-plugins.md).
- `kind: StateProvider` — the server-side session store's own module type,
  registered by a plugin and referenced from `Gateway.spec.stateProvider.type`.
