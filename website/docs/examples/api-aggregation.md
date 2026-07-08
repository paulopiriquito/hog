# Aggregate multiple backends

An `api` terminal fans a single inbound request out to one or more backends
concurrently and merges their JSON responses into one object, keyed by a
`group` name you choose. This example builds a dashboard endpoint that
combines a profile service and an orders service into one response.

!!! note "Prerequisites"
    A Go toolchain (`go run ./cmd/hog`) and Python 3 for two throwaway JSON
    backends. Docker works too, but running two extra backend containers on
    the same network is more setup than this example needs.

## 1. Stand up two toy backends

```sh
mkdir -p backend-a/v1 backend-b/v1
printf '{"id":"42","name":"Ada Lovelace"}' > backend-a/v1/profile
printf '[{"id":"1001","total":29.99},{"id":"1002","total":14.50}]' > backend-b/v1/orders
(cd backend-a && python3 -m http.server 9000) &
(cd backend-b && python3 -m http.server 9100) &
```

`terminal/api` only requires a 2xx response with a valid JSON body — it
doesn't check `Content-Type` — so a plain file server is enough to stand in
for a real backend here.

## 2. Write `gateway.yaml`

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
---
kind: Route
metadata: { name: dashboard }
spec:
  match: /api/dashboard
  handler:
    type: api
    timeout: 3s
    backends:
      - group: profile
        upstream: http://localhost:9000
        path: /v1/profile
        method: GET
      - group: orders
        upstream: http://localhost:9100
        path: /v1/orders
        method: GET
        required: false
        forwardQuery: true
  policy: { auth: public }
```

- **`backends[].group`** is required and unique — it's the response's merge
  key and must not collide across backends.
- **`required`** defaults to `true`: if the `profile` backend fails (network
  error, timeout, non-2xx, or an invalid/empty JSON body), the whole request
  fails. `orders` is marked `required: false`, so a failure there degrades
  gracefully instead.
- **`forwardQuery: true`** passes the inbound request's query string through
  to that backend only — useful for pagination params the frontend already
  sends (e.g. `?page=2`).
- **`timeout: 3s`** bounds the whole fan-out; each backend call runs inside
  the same 3-second budget.
- `policy: { auth: public }` keeps this example runnable without an IdP. A
  real deployment would typically pair this with `auth: required` — see
  [A BFF with OIDC login](bff-oidc.md).

## 3. Run it

```sh
go run ./cmd/hog --config gateway.yaml
```

## 4. Call it

```sh
curl -s http://localhost:8080/api/dashboard | python3 -m json.tool
```

!!! success "Result"

    ```json
    {
      "orders": [
        { "id": "1001", "total": 29.99 },
        { "id": "1002", "total": 14.5 }
      ],
      "profile": { "id": "42", "name": "Ada Lovelace" }
    }
    ```

    One JSON object, keyed by `group`, assembled from two concurrent
    backend calls.

## 5. See partial failure in action

Kill the `orders` backend (`kill %2`, or `Ctrl+C` its terminal) and repeat
the request:

```sh
curl -si http://localhost:8080/api/dashboard
```

You still get `200` with the `profile` key present; `orders` is omitted, and
the response carries `X-Hog-Partial: orders` so the caller can tell the
result is incomplete. Now kill the `profile` backend too (it's `required`)
and the whole request fails: `502 Bad Gateway` (or `504 Gateway Timeout` if
the backend was reachable but the 3-second budget expired).

## Path parameters

A backend's `path` can reference `{name}` segments captured from the route's
own match pattern — useful when the aggregation endpoint itself is
parameterized:

```yaml
spec:
  match: /api/users/{id}/dashboard
  handler:
    type: api
    backends:
      - group: profile
        upstream: http://localhost:9000
        path: /v1/users/{id}
```

An unmatched, empty, or path-traversing substitution (`.`, `..`, or a value
containing `/`) fails that backend closed rather than reaching an
unintended upstream path.

## Next steps

- Require a session before this route is reachable:
  [A BFF with OIDC login](bff-oidc.md).
- Add a group- or claim-based policy on top:
  [Enforce authorization](authorization.md).
- Full `api`/`backends` field list: the
  [configuration reference](../operations/configuration.md).
