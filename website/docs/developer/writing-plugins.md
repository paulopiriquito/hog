# Writing plugins

A HOG plugin is an ordinary Go package that registers itself with the
[module registry](../overview/concepts.md#plugins-the-registry) when it's
imported. There's no plugin interface to implement and no separate manifest
file describing your code — the package's `init()` function *is* the
registration, and a config resource's `type` field is what selects it at
boot.

This page walks through writing a **terminal handler** end to end, then lists
the other extension kinds available.

## The `registry.Factory` contract

Every kind of module — built-in or plugin — registers under the same shape: a
`kind` (a constant from the `config` package), a `name` (the config's `type`
value), and a `registry.Factory`:

```go
// Factory builds a configured module instance. instanceName is the resource's
// metadata.name, useful for error messages and logging.
type Factory func(instanceName string, cfg RawConfig) (any, error)
```

`hog.Register(kind, name, f)` (a re-export of `registry.Register`) adds `f` to
the process-wide registry. Call it from `init()` so registration happens as a
side effect of importing your package — see
[Framework mode](framework-mode.md) for why that matters for blank imports.

A factory returns `any` because the registry is generic across kinds; what
the *caller* does with the result depends on the kind. For
`config.KindTerminalHandler`, the app asserts the result implements
`http.Handler` and fails the build if it doesn't.

## Decoding config: `RawConfig.Decode`

Each resource's `spec` (or, for a terminal handler, the whole `handler:`
mapping) is kept as an undecoded `yaml.Node` until your factory runs. `cfg`
arrives as a `registry.RawConfig` wrapping that node; decode it into your own
typed struct with `Decode`:

```go
// Decode unmarshals the raw spec into v. A zero RawConfig decodes to nothing.
func (c RawConfig) Decode(v any) error
```

For a terminal handler specifically, the config node you receive is the
**entire** `handler:` mapping, including the `type` key that selected your
factory in the first place:

```yaml
handler:
  type: greeter   # selects your factory; also present in the node you decode
  message: hi there
```

Your struct only needs the fields it cares about — don't add a `Type` field
and don't enable `yaml.Node`'s strict `KnownFields(true)` mode when decoding.
The decoder tolerates the extra `type` key by default; turning on strict mode
would break every handler config, since none of them declare `type` as a
field of their own spec struct.

## A complete terminal handler plugin

This mirrors the shape of the built-in `static` handler
(`terminal/static.go`): a package-level `spec` struct, an `init()` that
registers a factory, and a handler type that does the work.

```go
// Package greeter registers a "greeter" terminal handler that writes a
// configured message as the response body.
package greeter

import (
	"fmt"
	"net/http"

	"github.com/paulopiriquito/hog"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// spec is the decoded `handler` config for `type: greeter`.
type spec struct {
	Message string `yaml:"message"`
}

func init() {
	hog.Register(config.KindTerminalHandler, "greeter", func(name string, cfg registry.RawConfig) (any, error) {
		var s spec
		if err := cfg.Decode(&s); err != nil {
			return nil, fmt.Errorf("greeter %q: %w", name, err)
		}
		if s.Message == "" {
			s.Message = "hello"
		}
		return &handler{message: s.Message}, nil
	})
}

// handler writes the configured message to every request.
type handler struct{ message string }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, h.message)
}
```

Route a path to it like any built-in handler:

```yaml
kind: Route
metadata: { name: hello }
spec:
  match: /hello
  handler:
    type: greeter
    message: "hi from HOG"
  policy: { auth: public }
```

A few things worth calling out, all straight from the built-ins:

- **Fail fast in the factory, not in `ServeHTTP`.** The built-in `static`
  handler opens its directory once in the factory (`os.OpenRoot`) so a bad
  `dir` fails the build instead of every request. Validate your config the
  same way.
- **The factory runs once per instance, at boot.** Anything expensive
  (opening a file, parsing config, building a client) belongs there, not in
  `ServeHTTP`.
- **`instanceName` is for diagnostics.** Use it in error messages, as the
  `static` handler does (`fmt.Errorf("static %q: %w", name, err)`), so a
  misconfigured instance is easy to trace back to its resource.

## Self-registration and blank imports

Your package must register in `init()` and never be imported for its
exported API — callers only need the side effect, as in this minimal test
fixture (`internal/hogbuild/testdata/plugin/plugin.go`), which does nothing
but register:

```go
package plugin

func init() {
	hog.Register(config.KindTerminalHandler, "testecho", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("plugin-ok"))
		}), nil
	})
}
```

To compile a plugin package into a binary, blank-import it next to
`hog.Main()` in [framework mode](framework-mode.md), or list its import path
in the `Gateway` resource's `plugins` manifest and let
[`hog-build`](building-binaries.md) generate that import for you. Both paths
run the same `init()`.

## Other extension kinds

Terminal handlers are the most common plugin, but the same `Factory` pattern
registers other kinds — each keyed by a `config.Kind*` constant and asserted
to a specific interface by the app:

- **`config.KindRequestPlugin` / `config.KindResponsePlugin`** — middleware
  that runs in the two guarded slots of the request chain (after the
  built-in gates, before the terminal; and as the response unwinds). A
  factory for these kinds must return a `chain.Middleware`
  (`Wrap(next http.Handler) http.Handler`); `chain.Func` adapts a plain
  function. See [core concepts: the middleware chain](../overview/concepts.md#the-middleware-chain).
- **`config.KindStateProvider`** — a server-side session store. HOG ships no
  storage backend; you implement `session.StateStore` (`Get`/`Set`/`Delete`
  over opaque, already-encrypted bytes — your plugin never sees plaintext)
  and register it under this kind. HOG encrypts every record before your
  `Set` and decrypts after your `Get`.
- **`config.KindIdP`** — an identity-provider connector implementing
  `idp.IdP`. HOG ships a built-in OIDC connector
  (`idp.Register`); registering your own under a different `name` lets a
  route select it the same way.

All four follow the factory shown above: decode `RawConfig` into a typed
struct, validate eagerly, return a value satisfying the kind's expected
interface.
