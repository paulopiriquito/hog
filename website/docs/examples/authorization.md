# Enforce authorization

Authentication answers *who is this request*; authorization answers *may
this request do this*. This example attaches two `kind: Policy` resources —
a built-in group requirement and an embedded Rego rule — to a protected
route, and shows a request that's allowed alongside one that's denied.

!!! note "Prerequisites"
    An OIDC provider that issues `Authorization: Bearer` access tokens for
    two test users: one in an `admins` group, one not. This example is
    cookieless (Bearer-only), so no session key or browser flow is needed —
    see [authentication and sessions](../design/auth-model.md#api-bearer-auth-the-non-browser-path)
    for why service routes accept Bearer tokens directly.

## 1. Write the Rego policy

Authorization denies are OPA/Rego evaluated at the `data.hog.authz.deny`
entrypoint: an empty deny set allows, any non-empty set denies. Save this as
`policies/no-destructive-writes.rego`:

```rego
package hog.authz
import rego.v1

deny contains msg if {
	input.request.method == "DELETE"
	msg := "DELETE is not permitted through this gateway"
}
```

## 2. Write `gateway.yaml`

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  identity:
    groups:
      source: isMemberOf
      match: ["cn=admins,ou=groups,dc=example,dc=com"]
      render: cn
      as: groups
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
kind: Policy
metadata: { name: admins-only }
spec:
  require:
    groups: [admins]
---
kind: Policy
metadata: { name: no-destructive-writes }
spec:
  rego:
    path: /etc/hog/policies/no-destructive-writes.rego
---
kind: Route
metadata: { name: admin-api, labels: { tier: admin } }
spec:
  match: /admin/
  handler:
    type: reverse-proxy
    upstream: http://admin-backend:9000
    stripPrefix: /admin
  policy: { auth: required }
  policies: [admins-only]
---
kind: RouteGroup
metadata: { name: admin-writes }
spec:
  selector: { matchLabels: { tier: admin } }
  policies: [no-destructive-writes]
```

- `identity.groups` maps an `isMemberOf`-style userinfo/token claim (common
  on LDAP-backed providers) into the session's `groups`: keep only DNs
  matching `cn=admins,...`, render each as its `cn=` value (`admins`).
- **`admins-only`** is attached directly to the route's own `policies:`.
- **`no-destructive-writes`** is attached to every route the `admin-writes`
  `RouteGroup` selects — here, any route labeled `tier: admin` — so it's
  configured once and inherited by every admin route without repeating it
  per-route.
- The effective policy set for a request is the union of the route's own
  `policies` and every matching group's, deduplicated; **all** of them must
  pass (deny-overrides) — `admin-api` is denied if either policy denies it.
- `rego.path` points at a file (or directory) *inside the running
  container* — mount `policies/` alongside your config, e.g.
  `-v "$(pwd)/policies:/etc/hog/policies:ro"`.

## 3. Run it

```sh
docker run --read-only --tmpfs /tmp \
  -p 8080:8080 \
  -e OIDC_CLIENT_SECRET \
  -v "$(pwd)/gateway.yaml:/etc/hog/gateway.yaml:ro" \
  -v "$(pwd)/policies:/etc/hog/policies:ro" \
  hog-runtime
```

## 4. Send an allowed request

A GET from a token whose subject is in `admins`: `admins-only` passes (group
present), and `no-destructive-writes` never fires (method isn't `DELETE`).

```sh
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/admin/users
```

!!! success "Result"
    `200` — HOG proxies the request to `admin-backend`.

## 5. Send two denied requests

A GET from a token *not* in `admins` fails `admins-only`:

```sh
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $USER_TOKEN" \
  http://localhost:8080/admin/users
```

A DELETE from the admin token still passes `admins-only` but is denied by
the Rego policy — group membership doesn't override it:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/admin/users/42
```

!!! success "Result"
    Both return `403 Forbidden` with a generic body — no policy name or
    reason. HOG logs which policy denied the request and why (subject,
    route, reason, trace ID) so operators can debug it without exposing that
    detail to the caller.

## Next steps

- Add the login flow this Bearer-only example skips:
  [A BFF with OIDC login](bff-oidc.md).
- Full `Policy`/`require`/`rego` field list and the decision model: the
  [configuration reference](../operations/configuration.md) and
  [authorization](../operations/authorization.md).
