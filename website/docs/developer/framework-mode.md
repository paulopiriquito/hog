# Framework mode

If you already write Go, HOG is just a package. Import it, blank-import your
plugin packages so their `init()` functions register, and call `hog.Main()`.
There's no build tool involved — you own the `main` module and build it with
plain `go build`.

## A complete `main.go`

```go
package main

import (
	"github.com/paulopiriquito/hog"

	_ "github.com/acme/hog-geoblock" // blank-import each plugin; its init() registers it
	_ "github.com/acme/hog-audit"
)

func main() { hog.Main() }
```

```sh
go build -o hog .
./hog --config gateway.yaml
```

The blank imports (`_ "github.com/acme/hog-geoblock"`) exist only for their
side effect: each plugin package's `init()` calls `hog.Register` against the
process-wide registry before `main` runs. Nothing else in this file needs to
reference the plugin.

## What `hog.Main` does

`hog.Main()`, in the root `hog` package, is the standard entrypoint:

1. Parses a `--config` flag (default `/etc/hog`) naming a config file or a
   directory of `*.yaml`/`*.yml` files.
2. Installs a context cancelled by `SIGINT`/`SIGTERM`, so the process shuts
   down cleanly on a normal stop signal.
3. Calls `hog.Run(ctx, path)` and exits non-zero on error.

If you need more control — a different flag set, your own signal handling, or
to run HOG alongside other code in the same process — call `hog.Run` directly
instead of `hog.Main`:

```go
// Run loads config from path and serves until the context is cancelled.
func Run(ctx context.Context, path string) error
```

`hog.Run` registers the built-in terminal handlers and the built-in IdP
connector, loads and parses the config at `path`, builds the app, and serves
until `ctx` is cancelled. Use it when you're embedding HOG inside a larger
program that already manages its own lifecycle and context.

## The registry your plugins register against

Both `hog.Main` and `hog.Run` build against `registry.Default`, a single
process-wide `*registry.Registry`. `hog.Register` is a thin re-export so
plugin authors only need to import the root `hog` package, not `registry`
directly:

```go
// Register registers a module on the default registry. Call it from a
// plugin's init(). Re-exported so plugin authors import only this package.
func Register(kind, name string, f registry.Factory) { registry.Register(kind, name, f) }
```

Because registration happens in `init()`, order between plugin packages
doesn't matter — Go runs every imported package's `init()` before `main`
starts, and `hog.Run` doesn't build any module until it parses your config.
A duplicate `(kind, name)` registration panics at startup, so a naming
collision between two plugins surfaces immediately rather than silently
shadowing one of them.

## Framework mode vs. `hog-build`

Both paths compile the same set of modules into one static binary; the
difference is who drives the `go build`:

- **Framework mode** gives you a normal Go module: your own `go.mod`, your own
  CI, `go build`/`go test`/`go vet` exactly like any other Go program. Prefer
  it if your team already has a Go build pipeline, wants to unit-test
  `main`-adjacent wiring, or needs to do something `hog-build` doesn't support
  (custom flags, embedding other logic in the same binary).
- **`hog-build`** generates the `main.go` and drives `go build` for you from a
  declarative plugin list in the `Gateway` resource, so a team without a Go
  build pipeline can still compose a custom binary. See
  [Building a custom binary](building-binaries.md).

Plugin code is identical either way — see [Writing plugins](writing-plugins.md).
