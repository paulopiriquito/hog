# Native Go, standard-library-first

HOG v1 was a fork of KrakenD, built on KrakenD's core framework, Lura. HOG v2
is a clean-room rewrite: no v1 code was reused, and the dependency on Lura was
dropped. v2 is built directly on Go 1.26 and `net/http`.

## Why not a framework

The traditional case for adopting a framework is that it owns the
security-sensitive plumbing — routing, traversal-safe file serving, CSRF
protection, cookie crypto — so an application doesn't have to get it right
itself. Recent Go releases moved most of that plumbing into the audited
standard library, which removes the main reason to pay for a framework:

| HOG job | Standard-library capability |
|---|---|
| Routing | `http.ServeMux` method-and-pattern routing, `r.PathValue()` |
| Traversal-safe static serving | `os.Root` + `http.FileServerFS`, `embed.FS` |
| Reverse proxying | `httputil.ReverseProxy` with `Rewrite` |
| CSRF protection | `http.CrossOriginProtection` (token-less, Fetch-metadata based) |
| Session encryption | `crypto/aes` with GCM |
| Structured logging | `log/slog` |
| Container-aware scheduling | `GOMAXPROCS` that reads cgroup limits |

Where the standard library doesn't reach, HOG adds a small, deliberate set of
dependencies: `coreos/go-oidc` and `golang-jwt` for OIDC and JWT handling, the
embedded Open Policy Agent engine for Rego-based authorization, and the
OpenTelemetry Go SDK for tracing and metrics. Each is scoped to the package
that needs it rather than pulled into a shared core.

## Alternatives considered

Three paths were evaluated before settling on native Go:

- **Lura** (KrakenD's core) was rejected because it's shaped for API
  composition rather than serving a web application, pulls in a web framework
  (gin) HOG doesn't need, and relies on Go's `plugin`/`.so` mechanism for
  extensibility — the same fragile, version-locked distribution model v2
  exists to move away from. Building on Lura would have meant keeping most of
  what v1's fork left behind.
- **Caddy** was a strong functional fit, but adopting it as a module host
  would make HOG a Caddy distribution — tied to Caddy's release cadence,
  dependency weight, and module conventions, with less control over the
  request path than owning it directly.
- **Traefik** extends through interpreted or WASM plugins rather than
  compiled Go, so it can't be embedded as a Go framework the way HOG's
  import-and-call-`Main()` mode requires.

## The trade-off

Going native means HOG owns and maintains more of its own machinery — the
middleware chain, the module registry, the configuration loader — instead of
inheriting it from a framework. In exchange, every request path is
transparent and auditable in HOG's own code, there's no external framework
release cadence to track or absorb breaking changes from, and the dependency
graph stays small and legible. This is a genuine cost: features a framework
would have provided for free (session middleware, plugin hosting, templating)
are built and tested in-house. HOG accepts that cost to keep the binary lean
and its behavior fully owned.

## Compile-time composition, not dynamic plugins

Every feature in HOG — including HOG's own first-party features like static
serving, the reverse proxy, and authorization — is a module: a Go package
that registers a factory into a shared registry, keyed by kind and name, at
`init()`. Configuration selects which modules to instantiate and how.
Composition happens once, at build time; nothing is loaded at runtime.

This rules out Go's `plugin`/`.so` loading mechanism, which requires an exact
toolchain and dependency match between the host binary and the plugin,
degrades in practice across Go versions, and imposes per-call indirection.
Compile-time composition instead gives every plugin the full Go language,
pins exact dependency versions in `go.sum`, and adds zero per-request
overhead — the trade-off is that adding or changing a plugin requires a
rebuild rather than a hot-swap.

## One binary, two ways to build it

The same compile-time model produces a single, statically linked binary,
distributed two ways: import HOG as a Go framework and call `hog.Main()`, or
declare plugins in a `Gateway` resource's manifest and let a build tool
compose the binary for you. Both converge on the same artifact — see
[delivery](delivery.md) for how the manifest drives that build, and
[developer: building a custom binary](../developer/building-binaries.md) for
the how-to.
