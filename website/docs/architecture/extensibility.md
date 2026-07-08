# Extensibility

**Everything HOG runs is a module.** A module is a Go value — a
`chain.Middleware`, an `http.Handler`, a `session.StateStore`, an `idp.IdP` —
built by a named factory registered on a single process-wide registry. The
`registry` package maps a `(kind, name)` pair to a `Factory`:

```go
type Factory func(instanceName string, cfg RawConfig) (any, error)
func Register(kind, name string, f Factory)
func (r *Registry) Build(kind, name string, cfg RawConfig) (any, error)
```

`RawConfig` wraps an undecoded YAML node, so each factory decodes its own
typed config struct — the registry itself never needs to know a module's
shape. Configuration selects modules by name: a route's `handler.type` builds
a `TerminalHandler`, a plugin resource's `spec.type` builds a `RequestPlugin`
or `ResponsePlugin`, and the `Gateway`'s `stateProvider.type` builds a
`StateProvider`.

## Built-ins are modules too

HOG's own terminal handlers — `health`, `static`, `reverse-proxy`, `api` —
and its built-in `oidc` IdP connector are registered on this exact same
contract; nothing about them is privileged. The difference is only *how* they
get registered: `hog.Run` calls `terminal.Register(reg)` and
`idp.Register(reg)` explicitly, so every HOG binary carries them regardless
of which plugins were compiled in.

The middleware **skeleton**, by contrast, is not a module at all — it is
hard-coded in `chain.Skeleton` at fixed positions in the chain (see
[request lifecycle](request-lifecycle.md)). No module, first-party or
third-party, can reorder it or run ahead of its gates. Developer code only
ever runs in the two guarded slots — `request-plugin` and `response-plugin`
— or as a terminal handler, IdP, or state provider that a route or the
gateway explicitly selects.

## Compile-time only

A module's factory registers itself from a package's `init()` — there is no
runtime `.so`/plugin loading. A module exists in a binary only if its package
was imported, directly or with a blank `_` import, when that binary was
built. This trades runtime flexibility for full Go tooling, pinned dependency
versions, and no per-request dispatch overhead.

## Two delivery paths, one mechanism

Both ways of shipping a HOG binary reduce to the same thing: import the
built-ins, blank-import your plugin packages so their `init()`s register, and
`go build`.

**Framework mode.** `import "github.com/paulopiriquito/hog"`, blank-import
your plugin packages, and call `hog.Main()` (or drive `hog.Run` yourself).
You own `main.go` and `go.mod` directly — this is how you'd embed HOG inside
a larger Go program.

**Base image / build manifest.** List your plugin import paths (optionally
`@version`) under the `Gateway` resource's `spec.plugins`. The `hog-build`
CLI (`cmd/hog-build`, backed by `internal/hogbuild`) reads that manifest,
renders a throwaway `main.go` that blank-imports every listed package plus a
`go.mod` that pins the HOG module, and runs `go build`. The `hog-builder`
base image packages this as a Dockerfile build stage, so a custom HOG image
is built the same way the base `hog-runtime`/`hog-static` images are. In this
mode, the config *is* the build manifest: the same `Gateway` resource that
configures the running gateway also decides what gets compiled into it.

Continue with [writing plugins](../developer/writing-plugins.md) for the
plugin contracts and package layout, or
[design: delivery](../design/delivery.md) for the reasoning behind
compile-time-only extensibility.
