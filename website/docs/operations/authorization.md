# Configure authorization

Authorization in HOG is a separate layer from authentication: a `Route` can
require a session (or Bearer token) *and* be gated by one or more named
`kind: Policy` resources that decide whether that particular principal may
proceed. A policy combines two optional tiers — a built-in `require` rule
and an embedded OPA/Rego rule — and is referenced by name from a `Route` or
`RouteGroup`.

## Write a policy

A `Policy` needs at least one of `require` or `rego`:

```yaml
kind: Policy
metadata: { name: admins-only }
spec:
  require:
    groups: [admins]
```

`require`:

- `groups` — any-of: the principal must belong to at least one listed
  group.
- `claims` — all-of across keys; each value is a scalar or a list (any-of
  within that claim):

```yaml
kind: Policy
metadata: { name: gold-tier }
spec:
  require:
    claims:
      tier: [gold, platinum]
```

A `require` block with neither `groups` nor `claims` is a **config error at
startup**, not a silent allow-all — this also catches a typo'd field name
(e.g. `group:` instead of `groups:`), which would otherwise decode to an
empty, always-satisfied rule.

## Apply it to a route

```yaml
kind: Route
metadata: { name: admin-panel, labels: { tier: app } }
spec:
  match: /admin/
  handler: { type: static, dir: /srv/admin }
  access:
    auth: required
    authorize: [admins-only]
```

Or to every route a `RouteGroup` selects:

```yaml
kind: RouteGroup
metadata: { name: admin-routes }
spec:
  selector:
    matchLabels: { area: admin }
  access:
    auth: required
    authorize: [admins-only]
```

A route's effective policy set is the **union** of its own `access.authorize`
and every matching `RouteGroup`'s `access.authorize` (deduplicated by name).
A route with an empty effective set skips the authorization gate entirely —
**default-allow**: authorization is opt-in per route, not a global gate.

## Write a Rego policy

For anything `require` can't express, point `rego.path` at a file or
directory of `.rego` source (resolved relative to where you run HOG /
mount your config):

```yaml
kind: Policy
metadata: { name: no-weekend-deletes }
spec:
  rego:
    path: /etc/hog/policies/no-weekend-deletes.rego
```

The module **must** define a `deny` rule under `package hog.authz`, queried
as `data.hog.authz.deny`:

```rego
package hog.authz

import rego.v1

deny contains msg if {
	input.request.method == "DELETE"
	not "admins" in input.groups
	msg := "DELETE requires admins"
}
```

HOG rejects the policy at startup (fail-fast, not fail-open) if the path has
no `.rego` modules, or if it never defines `data.hog.authz.deny` — a wrong
package name, a typo'd rule name, or an `allow`-only file are all startup
errors rather than a policy that silently evaluates to "no denies."

### What's in `input`

| Field | Description |
|---|---|
| `input.subject` | The principal's subject (`""` if unauthenticated). |
| `input.groups` | The principal's projected groups (`[]` if none/unauthenticated). |
| `input.claims` | The principal's passport claims (`{}` if none/unauthenticated). |
| `input.request.method` | HTTP method. |
| `input.request.path` | Request path. |
| `input.request.route` | The matched `net/http` pattern. |
| `input.request.route_name` | The `Route` resource's `metadata.name`. |
| `input.request.labels` | The route's `metadata.labels`. |

These keys are always present (empty rather than omitted for an
unauthenticated request), so a Rego rule like `not "admins" in input.groups`
evaluates as expected instead of becoming undefined. The principal's access
token is never included in `input`.

## How a decision is reached

- **Within one policy:** both tiers must pass. A satisfied `require` does
  **not** short-circuit a denying `rego` — if both are configured, both must
  allow.
- **Across a route's policy list:** any policy that denies stops evaluation
  and denies the request — **deny-overrides**.
- **Errors fail closed.** A Rego evaluation error (including a malformed
  `deny` — e.g. `deny := true` instead of a set) is treated as a deny, never
  silently as an allow.
- **The response is generic.** A denied request gets a plain `403 forbidden`
  body with no policy detail. The deny reason — which policy, and why — is
  logged (`slog`, at `Info` for a normal deny or `Error` for an evaluation
  error) and recorded as a span event (`authz.deny`), never returned to the
  client. Check your logs, not the response body, when debugging a `403`.

```yaml
spec:
  access:
    authorize: [gold-tier, no-weekend-deletes]
```

See [troubleshooting](troubleshooting.md#403-forbidden-from-a-policy) for
diagnosing an unexpected `403`, and
[observability](observability.md) for how the deny reason surfaces in
traces and logs.
