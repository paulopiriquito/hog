# Migrating from v1

HOG v1 is a fork of KrakenD-CE. HOG v2 is a clean-room rewrite on Go 1.26 and the
standard library — a native application gateway that is **no longer a fork of KrakenD**.
Because v2 shares no configuration format or extension model with v1, migration is a
re-platforming, not an in-place upgrade.

!!! warning "No automatic migration"
    There is no tool that converts a v1 `krakend.json` into v2 YAML. Rebuild your
    configuration against the v2 [configuration reference](../operations/configuration.md).

## What changed

| Area | HOG v1 (KrakenD fork) | HOG v2 (native Go) |
|---|---|---|
| Core | Lura / KrakenD, gin | `net/http` + the Go 1.26 standard library |
| Configuration | KrakenD-style JSON | Kubernetes-style YAML resources (`kind`/`metadata`/`spec`) with `${ENV}` |
| Endpoints | `endpoints` + `backend` blocks | `Route` resources with a `handler` (`static`, `reverse-proxy`, `api`) |
| Grouping | per-endpoint config | `RouteGroup` with label selectors |
| Auth | KrakenD auth plugins | Built-in BFF: OIDC login, encrypted cookie session, bearer tokens |
| Authorization | JWT/scope validators | `kind: Policy` — built-in group/claim rules + embedded OPA/Rego |
| Observability | KrakenD telemetry stack | Opt-in OpenTelemetry (OTLP) + trace-correlated access log |
| Extensions | Runtime `.so` plugins | Compile-time Go plugins (`hog-build`) or framework import |
| Distribution | KrakenD binary/image | `hog-build` + the `hog-runtime`/`hog-static` image family |

## How to migrate

1. **Model your endpoints as routes.** Each v1 endpoint becomes a `Route`. Static
   content maps to a `static` handler; a single backend maps to `reverse-proxy`; an
   endpoint that merges several backends maps to `api`. See the
   [configuration reference](../operations/configuration.md) and
   [API aggregation example](../examples/api-aggregation.md).
2. **Move authentication into the BFF.** Replace JWT-validator middleware with the
   built-in OIDC login and cookie session. See
   [configure authentication](../operations/authentication.md).
3. **Rewrite authorization as policies.** Replace scope/role checks with `kind: Policy`
   resources (built-in `require` or Rego) referenced from routes and route groups. See
   [configure authorization](../operations/authorization.md).
4. **Re-enable observability.** Configure OpenTelemetry export in the `Gateway`
   telemetry block. See [configure observability](../operations/observability.md).
5. **Port custom plugins.** Rewrite any v1 `.so` plugin as a compile-time Go module
   that self-registers, and compose it in with `hog-build`. See
   [writing plugins](../developer/writing-plugins.md) and
   [building a custom binary](../developer/building-binaries.md).
6. **Repackage.** Base your image on `hog-runtime` (or `hog-static` for a SPA), or
   compose a custom binary with the `hog-builder` → `hog-runtime` two-stage build.

## v1 deprecation

- **v1 is deprecated** as of the v2.0.0 release. It receives no new features.
- v1 remains in the repository under `v1/` as an archive for reference during
  migration. It is not maintained.
- Plan your migration to v2. New deployments should start on v2.
