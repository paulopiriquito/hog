# Core concepts

HOG has a small vocabulary. Learn it once and every configuration file, log
line, and plugin API reads the same way.

## Resources

You configure HOG with Kubernetes-style YAML **resources**. Every resource has
an `apiVersion`, a `kind`, a `metadata` block (`name` and, optionally,
`labels`), and a `spec` whose shape depends on the kind:

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
---
kind: Route
metadata: { name: spa }
spec:
  match: /
  handler:
    type: static
    dir: /srv/web
  policy: { auth: public }
```

A config path is a single file or a directory of `*.yaml`/`*.yml` files,
decoded as one or more `---`-separated YAML documents. Directory files load
in lexical filename order, which becomes the document order used for plugin
ordering (see [below](#the-middleware-chain)).

Every value supports `${ENV}` interpolation, resolved once at boot. `${VAR}`
is required — startup fails if it's unset — while `${VAR:-default}` falls
back to `default`.

The kinds you'll use most: `Gateway` (the root resource — listen address,
trusted proxies, and the plugin manifest), `Route`, `RouteGroup`, and
`Policy`. Extension points — `RequestPlugin`, `ResponsePlugin`, `IdP`,
`StateProvider`, `TerminalHandler`, `Telemetry` — follow the same shape; see
the [configuration reference](../operations/configuration.md).

## Routes & route groups

A **Route** matches one path to a handler:

```yaml
kind: Route
metadata: { name: dashboard, labels: { tier: api } }
spec:
  match: /api/dashboard
  handler: { type: api, backends: [...] }
  policy: { auth: required }
```

A **RouteGroup** applies shared policy — authentication requirement, route
type, projection settings — to every route whose labels match its selector,
the same way a Kubernetes Service selects Pods:

```yaml
kind: RouteGroup
metadata: { name: app-auth }
spec:
  selector: { matchLabels: { tier: api } }
  policy: { auth: required }
```

A RouteGroup is a selector-based policy object, not a parent container — a
route doesn't belong to a group, it merely matches one or more of them.
Matching groups apply in document order, and a route's own `spec.policy`
always wins. An unset `auth` defaults from the route's type: `service`
routes (`reverse-proxy`, `api`) require authentication; `app` routes
(`static`, and anything else) are public.

## Terminals (handlers)

A **terminal** is the handler at the end of a route — named by
`handler.type`:

- **`static`** — traversal-safe file serving with single-page application
  (SPA) fallback to `index.html` for unmatched paths.
- **`reverse-proxy`** — a transparent, single-backend proxy. Streams
  responses (server-sent events, WebSockets) and forwards `X-Forwarded-*`.
- **`api`** — aggregation across 1..N backends fetched concurrently, merged
  into a JSON object keyed by backend name.
- **`health`** — a built-in system endpoint for liveness/readiness checks.

## The middleware chain

Every request passes through a fixed, ordered chain of middleware before it
reaches its terminal. Outer to inner:

1. **Recover** — catches panics, logs them with trace correlation, and
   returns `500` instead of crashing the process.
2. **Request-ID** — assigns or propagates `X-Request-Id`.
3. **Access log** — emits one structured, trace-correlated log line per
   request on the way out.
4. **Security** — a reserved stage for security headers and cross-site request
   forgery (CSRF) protection. It is a pass-through today; enforcement is planned.
5. **Session** — resolves the caller's identity from the session cookie or a
   bearer token.
6. **Auth gate** — blocks unauthenticated access to routes that require it
   (browser routes redirect to login; API routes get `401`).
7. **Authz** — evaluates the route's [Policy](#resources) resources; a deny
   returns `403`.
8. **Projection** — strips inbound `X-User-*` headers and injects the
   resolved identity as headers for the backend.

The chain then hands off to the route's **terminal**. This skeleton is fixed:
you can't reorder it or run code ahead of the gates. Your own code runs only
in two guarded slots — request plugins (after the gates, before the
terminal) and response plugins (as the response unwinds) — in the order
their resources appear in the config.

## Plugins & the registry

Every module — built-in or third-party — is a Go package that calls
`Register(kind, name, factory)` in an `init()` function against a single
compile-time **registry**. A configuration resource names a `kind` and
`name`; at boot, HOG looks up the matching factory and builds an instance
from the resource's `spec`. A duplicate `(kind, name)` registration panics at
startup, so conflicts surface at boot, not at request time. This is how
`handler.type: static` resolves to the built-in static terminal — and how a
plugin you write resolves the same way.

## The single binary

There's no runtime plugin loading — no `.so` files, no sidecar processes.
Built-in modules and any plugins you write are Go packages compiled together
into one static binary, either by the `hog-build` CLI (which reads the
`Gateway` resource's plugin manifest and generates the import glue) or by
importing HOG as a Go framework and blank-importing your plugin packages
yourself. Both paths produce the same artifact: a single binary that serves
static content, proxies and aggregates APIs, and enforces the whole
security chain — with no external control plane.
