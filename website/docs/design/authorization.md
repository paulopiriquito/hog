# Authorization

Authentication answers *who is this request*; authorization answers *may
this request do this*. HOG separates the two into distinct gates in the
middleware chain: by the time authorization runs, an unauthenticated request
to a protected route has already been rejected, and the resolved principal —
subject, passport claims, groups — is available.

## `kind: Policy`: two tiers, one resource

Authorization rules are named `kind: Policy` resources, each carrying one or
both of two tiers:

- **Built-in `require`** — zero-dependency RBAC/ABAC: `groups` (the principal
  must belong to at least one listed group) and `claims` (every listed claim
  must match, each value either a scalar or a list treated as "any of").
  This tier covers the common case — group- or attribute-gated routes —
  without pulling in a policy engine.
- **Embedded OPA/Rego** — arbitrary policy-as-code, evaluated by an embedded
  Open Policy Agent engine at the `data.hog.authz.deny` entrypoint. A policy
  denies by producing a non-empty set of deny reasons; an empty set allows.
  This tier exists for logic that outgrows simple group/claim matching —
  time-of-day rules, cross-field conditions, anything expressible in Rego.

A policy can declare either tier, or both — in which case both must pass. A
policy with neither is treated as a configuration mistake and fails at
startup, not on the first request that hits it.

## The decision model

Three rules govern how policies combine into a decision:

- **Additive, default-allow.** A route that references no policy is allowed
  on authentication alone — authorization is opt-in per route, layered on
  top of the authentication gate rather than replacing it.
- **Deny-overrides.** A route can reference several policies (its own, plus
  every matching route group's); if *any* of them denies, the request is
  denied. Policies can only add restrictions, never grant an exception to
  another policy's denial.
- **Fail-closed.** A `require` clause that needs an identity but finds no
  principal is unsatisfiable and denies, rather than skipping the check. A
  Rego evaluation error, or a malformed decision shape, also denies. When a
  decision can't be made safely, HOG denies rather than guesses.

## Why the response is a generic 403

A denied request gets a plain `403 Forbidden` with no policy detail in the
body — which policy fired and why is deliberately withheld from the caller.
The reason is written to the log instead, alongside the subject, route, and
denying policy name, correlated to the request's trace ID, plus a trace span
event. This keeps a caller from learning which of several possible
conditions blocked them (useful reconnaissance for an attacker probing
access boundaries) while giving operators everything they need to debug a
denial from logs and traces.

## Referencing policies from routes

Routes and route groups opt into authorization with a `policies:` list of
policy names — distinct from the existing `policy.auth` block, which governs
authentication. A route group's policies apply to every route its selector
matches, so a policy can be attached once and inherited broadly (an empty
selector scopes it to all routes); the effective set for a request is the
union of the route's own policies and every matching group's, deduplicated.
A policy name with no matching `Policy` resource fails the build.

```yaml
kind: Route
metadata: { name: admin, labels: { tier: api } }
spec:
  match: /admin/
  policies: [admins-only]
```

## Confinement: OPA stays in one package

Open Policy Agent is the single largest dependency HOG carries — embedding a
full policy engine is not free in binary size or build weight. To keep that
cost contained, only the `authz` package imports OPA. Routes and route
groups carry policy references as plain strings; the `route` package that
defines them has no dependency on `authz` or OPA at all. The dependency
graph is acyclic and one-directional: only the application's build step
bridges policy names to compiled policies, so the rest of the codebase — and
any code that doesn't need authorization — never pays for OPA's presence.

See [operations: authorization](../operations/authorization.md) for
configuration and Rego examples.
