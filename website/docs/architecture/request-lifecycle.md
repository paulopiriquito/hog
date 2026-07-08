# Request lifecycle

Every request that reaches HOG passes through the same shape: an outer
tracing wrapper, a route match, a fixed chain of built-in stages, two guarded
slots for developer plugins, and finally a terminal handler. The chain is
assembled once per route at boot (`app.Build`, `chain.Skeleton`) and applied
to every request that route serves.

Before routing, the whole `ServeMux` is wrapped once in an OpenTelemetry HTTP
handler. It starts (or continues, from an inbound `traceparent`) the request's
trace span and names it after the matched route. `ServeMux` then matches the
request's method and path to a `Route` and dispatches to that route's own
composed chain.

## The fixed skeleton

`chain/builtin.go` defines the built-in stages in a fixed order — outermost
first — that brackets every route:

```text
recover → request-id → access-log → security → session → auth-gate → authz → projection
```

**recover** catches a panic anywhere further in — including the terminal —
logs it with the request's trace and span IDs, and returns `500` instead of
crashing the process.

**request-id** reads an inbound `X-Request-Id` header or generates one, and
echoes it back on the response, so a request can be correlated across logs.

**access-log** wraps the response writer to capture the final status code and
emits one structured log line after the handler returns — method, path,
status, duration, and, when a `Telemetry` resource is configured, trace
correlation and additional fields.

**security** is a reserved slot for CSRF and security-header enforcement. In
the current build it is a pass-through with no effect; the enforcement itself
is scoped to a later spec.

**session** resolves the caller's identity into a request-scoped `Principal`.
For `app` routes it reads the encrypted session cookie. For `service` routes
it also accepts an `Authorization: Bearer` token, verified against the
configured IdP, when no valid cookie is present — the cookie always wins if
both are sent. A missing or invalid credential does not reject the request
here; it simply leaves it unauthenticated.

**auth-gate** enforces the route's effective `auth: required|public` setting.
This is the stage that turns a missing identity into a rejection: a browser
(`app`) route gets a `302` redirect to the login path with the original URL
preserved as `return_to`; a `service` route gets a `401` with a
`WWW-Authenticate` header.

**authz** evaluates the route's effective policy set — its own `policies`
plus every matching `RouteGroup`'s — against the resolved identity and the
request's attributes. Any policy that denies returns `403`. This stage runs
independently of whether a session or IdP is configured at all, since a
policy can match on request attributes alone.

**projection** strips any inbound `X-User-*` headers as an anti-spoofing
measure and, only when a principal is present in context, injects identity
headers for the backend to trust: a subject header, a groups header, and
either derived or explicitly mapped claim headers.

## The guarded plugin slots and the terminal

Two more positions exist after the fixed skeleton, both reserved for
developer code and both selector-matched against the route's labels, in YAML
document order:

```text
recover                                        ┐
  request-id                                   │
    access-log                                 │  fixed built-in skeleton
      security (reserved)                      │  (chain.Skeleton)
        session                                │
          auth-gate                            │
            authz                              │
              projection                       ┘
                request-plugins   (developer, YAML order)
                  response-plugins (developer, YAML order)
                    terminal handler
```

**request-plugins** run after every gate has passed and before the terminal —
they can inspect or short-circuit the fully-authenticated, fully-authorized
request.

**response-plugins** sit closest to the terminal, so on the way back out they
are the first to see the response — they shape the final status and content
(for example, reshaping an aggregated API response) before it unwinds back
through projection, authz, session, access-log, and out.

The terminal handler ends the chain: `static`, `reverse-proxy`, `api`
(aggregation), or an auth/system endpoint.

## Reserved slots activate only when configured

`session`, `auth-gate`, and `projection` come from `chain.Gates`, supplied by
`app.Build`. When neither a `session` block nor an `IdP` resource is
configured, `app.Build` leaves those fields nil and `chain.Skeleton` fills
them with a pass-through — an all-public gateway with no IdP costs nothing
extra at these stages. `authz` is decided independently, per route: it
activates only when that route, directly or through a matching `RouteGroup`,
references at least one `Policy` by name.

## Order consequences

Because **auth-gate runs before authz**, an unauthenticated request to a
protected route is redirected or rejected before any policy is evaluated —
authz never has to account for a missing identity.

Because **projection runs after authz**, identity headers are injected only
into a request that has already been allowed through. A request denied by
authz never reaches projection, and never reaches the backend.

See [authentication](../operations/authentication.md) and
[authorization](../operations/authorization.md) for how to configure the
session, IdP, and policy resources that fill these stages.
