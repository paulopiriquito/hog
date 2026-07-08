# Contributing

This page covers changes to HOG itself — the packages under the repo root —
not plugins that live in their own module. If you're building something that
depends on HOG rather than modifying it, see the rest of the
[developer guide](index.md) instead.

## Repo layout

The vanilla `hog` binary (`cmd/hog`) is `hog.Main()` with no plugins
blank-imported — every package below is either a built-in module or shared
plumbing:

| Package | Responsibility |
|---|---|
| `app` | Parses config into a typed `Config`, builds the `*App` (wires the middleware chain, routes, session, IdP). |
| `auth` | BFF auth endpoints (login/callback/logout), the session and bearer auth gates, identity projection. |
| `authz` | Compiles `Policy` resources and evaluates them (OPA-backed) as the authz gate. |
| `chain` | The fixed middleware skeleton (`chain.Middleware`, `chain.Compose`, `chain.Skeleton`) that request/response plugins slot into. |
| `config` | The generic `Resource`/`Metadata` shape, the `Kind*` constants, and multi-document YAML loading. |
| `gateway` | The `Gateway` resource: listen address, plugin manifest, session/telemetry/stateProvider blocks. |
| `idp` | The `IdP` interface and the built-in OIDC connector. |
| `registry` | The compile-time module registry (`Factory`, `RawConfig`, `Register`/`Build`, `Default`). |
| `route` | `Route`/`RouteGroup` parsing and policy resolution. |
| `selector` | Kubernetes-style label selector matching, used by `RouteGroup` and request/response plugin targeting. |
| `session` | `Session`, the cookie/stateful `Manager`s, the `Sealer` (AES-256-GCM), and the `StateStore` extension point. |
| `telemetry` | OpenTelemetry tracing/metrics setup and the trace-correlated access log. |
| `terminal` | Built-in terminal handlers: `static`, `health`, `reverse-proxy` (`proxy.go`), `api` (`aggregate.go`). |
| `cmd/hog` | The vanilla binary entrypoint — `hog.Main()`, nothing blank-imported. |
| `cmd/hog-build` | The `hog-build` CLI (flag parsing) over `internal/hogbuild`. |
| `internal/hogbuild` | Build-tool-only: renders the composed `main.go`/`go.mod` and drives `go build`. Not importable outside this module — see [Testing plugins](testing.md) for how a plugin repo tests against it via the CLI instead. |
| `build/` | The `hog-builder` → `hog-runtime` → `hog-static` Dockerfiles. |
| `examples/` | Reference material, e.g. `Dockerfile.custom` for a plugin image. |
| `website/` | This documentation site (mkdocs-material). |

`hog.go` at the repo root is the public framework API: `hog.Main`, `hog.Run`,
`hog.Register`. Keep it minimal — it's the surface every plugin author and
framework-mode consumer depends on.

## Conventions

- **Go 1.26**, no exceptions — check `go.mod` before assuming a newer or
  older language feature is available.
- **Standard-library-first.** `net/http`, `log/slog`, `os.Root`, and friends
  are preferred over a dependency. The current dependency list is
  deliberately short: OIDC (`coreos/go-oidc`), JWT (`go-jose`), policy
  evaluation (`open-policy-agent/opa`), OpenTelemetry, `golang.org/x/oauth2`,
  and `yaml.v3` for config. Don't add a package to reach for something the
  standard library already does.
- **Test-driven.** Every package above is developed test-first and ships
  with a colocated `_test.go` file per source file (see
  `terminal/static_test.go`, `session/statemanager_test.go`, and so on).
  Add tests before or alongside the code they cover, not after.
- **Format and vet before committing:**
  ```sh
  gofmt -l .
  go vet ./...
  go test ./...
  ```
  `go test -short ./...` skips the slower integration tests (like
  `internal/hogbuild`'s binary-composition test) for a fast inner loop; run
  the full suite, including `-race` for concurrency-sensitive packages,
  before opening a pull request.
- **Modules.** The repo is a single Go module at the root; `tests/e2e` and
  `internal/hogbuild/testdata/plugin` are nested modules with their own `go.mod`,
  so they're excluded from the root's `go build/test ./...`. The predecessor
  KrakenD-based gateway (v1) is not in the tree — it lives in git history before
  the `v2.0.0` release.
