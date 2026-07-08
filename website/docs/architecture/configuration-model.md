# Configuration model

HOG is configured entirely by YAML, in a Kubernetes-style resource shape.
Every document carries a `kind`, `metadata` (`name` plus optional `labels`),
and a `spec` — the module- or route-specific settings. There is no separate
schema file; each resource kind decodes its own `spec` into a typed Go struct
at load time.

## `${ENV}` expansion

Before any document is parsed, `config.Load` expands `${VAR}` and
`${VAR:-default}` references against the process environment, across the
entire raw file text — so a variable can appear anywhere, including inside a
quoted string. A referenced variable with no value and no default fails
config loading immediately; an explicitly-set empty value satisfies the
reference and skips the default.

## Multi-document files and directories

`config.Load` accepts either a single file or a directory. For a directory,
every `*.yaml`/`*.yml` file is read in **lexical filename order**; within a
file, YAML's `---` separates multiple documents. The concatenation of all
documents, in that order, is the config's *document order* — it decides two
things later: which `RouteGroup` field wins when several groups match the
same route (last match wins), and the default ordering of `RequestPlugin` and
`ResponsePlugin` instances that match the same route.

## Resource kinds

The top-level `kind` values a config document can declare are:

| Kind | Cardinality | Purpose |
|---|---|---|
| `Gateway` | exactly one | gateway-wide settings and the plugin build manifest |
| `Route` | many | binds a path pattern to a terminal handler |
| `RouteGroup` | many | applies shared type/auth/projection/policy to routes by selector |
| `Policy` | many | a named authorization rule, referenced by routes/groups |
| `IdP` | zero or one (today) | the OpenID Connect provider |
| `RequestPlugin` | many | a developer middleware in the pre-terminal slot |
| `ResponsePlugin` | many | a developer middleware in the post-terminal slot |
| `Telemetry` | zero or one | logging, tracing, and access-log settings (defaulted when absent) |

`TerminalHandler` and `StateProvider` are also names in HOG's kind namespace,
but they are not top-level document kinds you write yourself — they identify
modules selected *from inside* another resource: a route's
`spec.handler.type` picks a terminal handler, and the `Gateway`'s
`spec.stateProvider.type` picks a state-provider module. See
[extensibility](extensibility.md) for how the registry uses `kind` this way.

## The `Gateway` resource

Exactly one `Gateway` resource is required; `app.Parse` rejects a config with
zero or with more than one. Its `spec` holds:

- `listen` — the HTTP listen address (default `:8080`).
- `trustedProxies` — the load balancer/proxy addresses HOG runs behind.
- `plugins` — the build-time module manifest (import paths, optionally
  `@version`); consumed by `hog-build`, not read at runtime.
- `session` — cookie name, sealing key, TTL, fingerprint headers, the session
  info path, and the post-logout redirect.
- `identity` — how claims and groups are read out of the ID token/userinfo
  response (`claims`, `groups.source|match|render|as`, `userInfo`).
- `auth` — the login and logout endpoint paths.
- `stateProvider` — an optional server-side session store: `type`,
  `refreshSkew`, `keyPrefix`, and opaque `config` passed to that module.

## Routes and route groups

A `Route`'s `spec` sets `match` (the `ServeMux` pattern) and `handler`
(`type` plus handler-specific config). It may also set `type` (`app` or
`service`), a `policy` block (`auth`, `projection`), and a `policies` list of
`Policy` names to enforce.

A `RouteGroup` is **not** a parent container — it is a selector-based policy
object, exactly like a Kubernetes Service selecting Pods. Its `spec.selector`
(`matchLabels` and/or set-based `matchExpressions`) picks which routes it
applies to by their `metadata.labels`; an empty selector matches every route.
Its `type`, `policy`, and `policies` apply to every route it matches.

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
---
kind: Route
metadata: { name: dashboard, labels: { app: dash, tier: api } }
spec:
  match: /api/dashboard
  handler:
    type: api
    backends:
      - { group: profile, upstream: ${PROFILE_SVC}, path: /me }
      - { group: orders,  upstream: ${ORDERS_SVC},  path: /list }
  policies: [dash-users]
---
kind: RouteGroup
metadata: { name: app-auth }
spec:
  selector: { matchLabels: { app: dash } }
  policy: { auth: required }
---
kind: Policy
metadata: { name: dash-users }
spec:
  require:
    groups: [dashboard-users]
```

## Resolving a route's effective policy

`route.Resolve` computes what actually governs one route, combining the
route's own fields with every `RouteGroup` whose selector matches its labels
(document order; a later matching group overrides an earlier one field by
field):

- **Type** — the route's own `type` wins if set; otherwise the last matching
  group's `type`; otherwise it's inferred from the handler (`reverse-proxy`
  and `api` infer `service`; everything else infers `app`). The result must
  be `app` or `service`.
- **Auth** — the route's own `policy.auth` wins if set; otherwise the last
  matching group's; otherwise it defaults by type (`service` → `required`,
  `app` → `public`). The result must be `required` or `public`.
- **Projection** — the route's own `policy.projection` wins if set, otherwise
  the last matching group's.
- **Policies** — the *union* of the route's own `policies` list and every
  matching group's `policies` list, de-duplicated, in that order. This is the
  set the authz stage evaluates.

An invalid `type` or `auth` value fails the build, even on a route that would
otherwise run without a session or IdP configured.

See the [configuration reference](../operations/configuration.md) for the
full field-by-field listing.
