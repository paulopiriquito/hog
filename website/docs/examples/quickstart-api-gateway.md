# Quick start: an API gateway with authorization

Authentication answers *who is this request*; authorization answers *may
this request do this*. This guide builds a gateway that authenticates every
caller through OIDC, then layers two kinds of authorization on top: a
built-in policy that requires group membership, and a Rego rule that blocks
write methods for anyone who isn't an admin — composed per route and per
route group.

!!! note "Prerequisites"
    - An OIDC provider that issues `Authorization: Bearer` access tokens
      for two test users: one in an `admins` group, one in `staff` only.
      This guide tests with Bearer tokens directly (no browser), the same
      way as [Enforce authorization](authorization.md).
    - A JSON backend reachable at `http://backend:9000` — see
      [Aggregate multiple backends](api-aggregation.md) for a two-line
      stand-in, or bring your own.
    - Docker, and the `hog-runtime` image built locally, as in
      [Serve a static site](quickstart-static.md).

## Folder structure

```text
api-gateway/
├── gateway.yaml         # Gateway + session + IdP + Routes + Policy resources
├── policies/
│   └── writes.rego      # a Rego policy
└── Dockerfile
```

## 1. Write the Rego policy

Authorization denies are OPA/Rego evaluated at the `data.hog.authz.deny`
entrypoint: an empty deny set allows, any non-empty set denies. This rule
blocks write methods for anyone not in `admins`, regardless of what other
policy already let them through. Save this as `policies/writes.rego`:

```rego
package hog.authz
import rego.v1

deny contains msg if {
	input.request.method in {"POST", "PUT", "DELETE"}
	not "admins" in input.groups
	msg := "writes require admins"
}
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
  identity:
    groups:
      source: groups
      match: [admins, staff]
      render: dn
      as: groups
---
kind: IdP
metadata: { name: default }
spec:
  type: oidc
  issuer: ${OIDC_ISSUER}
  clientID: ${OIDC_CLIENT_ID}
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: http://localhost:8080/auth/callback
---
kind: Policy
metadata: { name: admins }
spec:
  require:
    groups: [admins]
---
kind: Policy
metadata: { name: staff }
spec:
  require:
    groups: [staff, admins]
---
kind: Policy
metadata: { name: writes }
spec:
  rego:
    path: /etc/hog/policies/writes.rego
---
kind: Route
metadata: { name: users-api, labels: { tier: api } }
spec:
  match: /api/users
  handler:
    type: reverse-proxy
    upstream: http://backend:9000
    stripPrefix: /api
  policy: { auth: required }
  policies: [staff]
---
kind: Route
metadata: { name: admin-api, labels: { tier: api } }
spec:
  match: /api/admin/
  handler:
    type: reverse-proxy
    upstream: http://backend:9000
    stripPrefix: /api
  policy: { auth: required }
  policies: [admins]
---
kind: RouteGroup
metadata: { name: api-writes }
spec:
  selector: { matchLabels: { tier: api } }
  policies: [writes]
```

- **`admins`** and **`staff`** are attached directly to a route's own
  `policies:` — `users-api` accepts anyone in `staff` *or* `admins`
  (broader, read-heavy endpoint); `admin-api` accepts only `admins`
  (narrower).
- **`writes`** is attached to every route the `api-writes` `RouteGroup`
  selects — here, any route labeled `tier: api` — so it's configured once
  and inherited by both routes without repeating it per-route.
- The effective policy set for a request is the union of the route's own
  `policies` and every matching `RouteGroup`'s, deduplicated; **all** of
  them must pass. A `staff` member passes `users-api`'s own `staff` policy
  but is still denied a `POST` there by `writes` — group membership on the
  route doesn't override the narrower Rego rule.

## 3. Write the Dockerfile

```dockerfile
FROM hog-runtime
COPY --chown=hog:hog gateway.yaml /etc/hog/gateway.yaml
COPY --chown=hog:hog policies/    /etc/hog/policies/
```

## 4. Build and run it

```sh
docker build -t api-gateway .

export SESSION_KEY=$(openssl rand -hex 16)
export OIDC_ISSUER=https://idp.example.com
export OIDC_CLIENT_ID=hog-example
export OIDC_CLIENT_SECRET=your-client-secret

docker run --read-only --tmpfs /tmp \
  -p 8080:8080 \
  -e SESSION_KEY -e OIDC_ISSUER -e OIDC_CLIENT_ID -e OIDC_CLIENT_SECRET \
  api-gateway
```

!!! success "Expected result"
    `"hog listening" addr=:8080` on stdout.

## 5. Send an allowed request

A `GET` from a token whose subject is in `admins`: `admins` passes (group
present), and `writes` never fires (`GET` isn't a write method).

```sh
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/api/admin/users
```

!!! success "Expected result"
    `200` — HOG proxies the request to `backend:9000`.

## 6. Send two denied requests

A `GET` from a token in `staff` only, against the admin-only route, fails
the route's own `admins` policy:

```sh
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $STAFF_TOKEN" \
  http://localhost:8080/api/admin/users
```

That same `staff` token passes `users-api`'s broader `staff` policy — but a
`POST` there is still denied by the `writes` Rego rule, because the caller
isn't in `admins`:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  -H "Authorization: Bearer $STAFF_TOKEN" \
  http://localhost:8080/api/users
```

!!! success "Expected result"
    Both return `403 Forbidden` with a generic body — no policy name or
    reason. HOG logs which policy denied the request and why:

    ```json
    {"time":"...","level":"INFO","msg":"authz denied","policy":"admins","reason":"require not satisfied","subject":"staff-user","route":"/api/admin/","method":"GET"}
    ```

    (An `$ADMIN_TOKEN` `POST` to `/api/users` returns `200` — the same
    Rego rule allows writes once the caller *is* in `admins`.)

## Where groups come from

`identity.groups` maps a claim on the ID token or userinfo response into
the session's `Groups`, which authorization then reads:

```yaml
identity:
  groups:
    source: groups   # claim name carrying the group list
    match: [admins, staff]
    render: dn        # keep each value as-is
    as: groups        # session field name (also the default X-User-Groups header)
```

- **`source`** is the claim holding the array — a plain `groups: ["admins",
  "staff"]` claim, common on Keycloak/Auth0/Okta, works directly with
  `render: dn` (keep the raw string). If your provider instead issues
  LDAP-style DNs (an `isMemberOf` claim, common behind on-prem AD), use
  `render: cn` to extract the `cn=` value — see
  [Enforce authorization](authorization.md) for that shape.
- **`match`** is a case-insensitive substring allowlist, not optional: an
  *empty* (or omitted) `match` keeps nothing, so every request's group list
  comes back empty and every group-gated policy fails closed. List every
  group name (or a safe, non-overlapping substring of it) you actually
  reference in a `Policy`.
- The result lands on `session.Principal.Groups`, which both the built-in
  `require: { groups: [...] }` check and `input.groups` in Rego read.

## How authorization decides

- **`require`** semantics: `groups` is *any-of* (the principal needs at
  least one listed group); `claims` is *all-of* across different claim
  keys, each with its own any-of value list.
- **Additive, default-allow**: a route with no `policies:` and no matching
  `RouteGroup` has no authorization gate at all — only the auth (`required`
  vs `public`) check applies.
- **Deny-overrides, fail-closed**: every policy in the effective set must
  pass; the first deny wins, and a Rego evaluation error is itself treated
  as a deny (never a silent allow).
- **Bearer and browser auth both populate the same `Principal`** — a
  cookie session and a verified `Authorization: Bearer` token project
  identically through `identity.claims`/`identity.groups`, so the same
  `Policy` resources gate both a browser-driven `app` route and an
  API-client `service` route without change.
- The `403` body never carries the policy name or reason — only the access
  log does, alongside the subject, route, and (when tracing is on) the
  trace ID, so operators can debug a denial without exposing why to the
  caller.

## Next steps

- The full login flow this guide tests around with Bearer tokens:
  [A BFF with OIDC login](bff-oidc.md) and
  [A Vue SPA and backend behind auth](quickstart-spa-backend.md).
- The LDAP-style `isMemberOf` group mapping, and a `RouteGroup` example
  with a hard-denied method: [Enforce authorization](authorization.md).
- Full `Policy`/`require`/`rego`/`identity` field list and the decision
  model: the [configuration reference](../operations/configuration.md) and
  [configure authorization](../operations/authorization.md).
