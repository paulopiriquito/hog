# Write and build a custom plugin

HOG has no runtime plugin loading — a plugin is a Go package whose `init()`
registers a module against a compile-time registry, and `hog-build` composes
it into one static binary alongside the built-ins. This example writes a
minimal terminal-handler plugin, declares it in the `Gateway.plugins`
manifest, and builds the binary with `hog-build` — no Go build pipeline of
your own required.

!!! note "Prerequisites"
    Docker and a local clone of the HOG repository, to build the
    `hog-builder`/`hog-runtime` base images (not published to a registry
    yet — same caveat as the [quick start](quickstart.md)).

## 1. Write the plugin

A terminal-handler plugin registers a factory under `config.KindTerminalHandler`
with `hog.Register`. The factory's signature is fixed:
`func(instanceName string, cfg registry.RawConfig) (any, error)`, returning
anything satisfying `http.Handler`. Save this as `plugin/plugin.go`:

```go
// Package helloplugin registers a minimal `hello` terminal handler.
package helloplugin

import (
	"fmt"
	"net/http"

	"github.com/paulopiriquito/hog"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// helloConfig is this handler's own `spec.handler` fields.
type helloConfig struct {
	Greeting string `yaml:"greeting"`
}

func init() {
	hog.Register(config.KindTerminalHandler, "hello", func(name string, cfg registry.RawConfig) (any, error) {
		var hc helloConfig
		if err := cfg.Decode(&hc); err != nil {
			return nil, fmt.Errorf("hello %q: %w", name, err)
		}
		if hc.Greeting == "" {
			hc.Greeting = "hello"
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s, %s\n", hc.Greeting, r.URL.Query().Get("name"))
		}), nil
	})
}
```

`cfg.Decode` unmarshals the route's `handler:` mapping — including the
`type` key, which your struct can safely ignore — into `helloConfig`. See
[writing plugins](../developer/writing-plugins.md) for the full contract
(module kinds, config decoding, testing your factory in isolation).

## 2. Declare it in the config

`Gateway.plugins` is both documentation and the build manifest — `hog-build`
reads this same file to know what to import:

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  plugins:
    - github.com/acme/hog-hello@v1.0.0
---
kind: Route
metadata: { name: hello }
spec:
  match: /hello
  handler:
    type: hello
    greeting: "hi there"
  policy: { auth: public }
```

Each entry is `<import-path>[@version]` — the package whose `init()` calls
`hog.Register`. While you haven't published `hog-hello` yet, keep the entry
unversioned and override it with `--replace` (below); a pinned `@version` is
what you'd use once it's a real, tagged module.

## 3. Build the base images once

```sh
docker build -f build/Dockerfile.builder -t hog-builder .
docker build -f build/Dockerfile.runtime -t hog-runtime .
```

## 4. Compose the custom binary

A two-stage Dockerfile: `hog-builder` composes the binary, `hog-runtime`
serves it. `--replace` swaps the unpublished `github.com/acme/hog-hello`
import for your local `plugin/` directory:

```dockerfile
# syntax=docker/dockerfile:1
FROM hog-builder AS build
COPY plugin/ ./plugin/
COPY gateway.yaml .
RUN hog-build --config gateway.yaml -o /out/hog \
  --replace github.com/acme/hog-hello=./plugin

FROM hog-runtime
COPY --from=build /out/hog /usr/local/bin/hog
COPY --chown=hog:hog gateway.yaml /etc/hog/gateway.yaml
```

`hog-runtime` already creates an empty, `hog`-owned `/srv/web` — this
example has nothing to serve from it, since its only route is `/hello`. Add
a `COPY --chown=hog:hog dist/ /srv/web/` line here if your plugin's config
also serves static content.

`hog-build` reads the manifest, generates a throwaway `main.go` that
blank-imports every listed plugin next to `hog.Main()`, and runs `go build`
pinned against the `hog` source the `hog-builder` image already carries
(`HOG_SOURCE=/src`, baked into that image at `docker build -f
Dockerfile.builder` time) — so the composed binary always contains *this*
`hog`, never whatever `go get` would otherwise resolve.

```sh
docker build -t my-hog .
docker run --read-only --tmpfs /tmp -p 8080:8080 my-hog
curl "http://localhost:8080/hello?name=Ada"
```

!!! success "Result"
    `hi there, Ada` — your plugin's handler, compiled into the same static
    binary as every built-in terminal.

## Local iteration without Docker

Outside a container, `hog-build` is a plain CLI: build it once from a clone
of the `hog` repo, then point `--hog-source` at that checkout (the
`hog-builder` image sets this for you via `$HOG_SOURCE`; bare CLI use needs
it explicitly):

```sh
cd /path/to/hog && go build -o hog-build ./cmd/hog-build
cd /path/to/your/plugin-project
/path/to/hog/hog-build --config gateway.yaml -o ./hog \
  --hog-source /path/to/hog \
  --replace github.com/acme/hog-hello=./plugin
./hog --config gateway.yaml
```

`--keep` retains the generated `main.go`/`go.mod` in a temp dir for
inspection after a failed build; `--tags`/`--go` pass through build tags and
a specific Go toolchain, respectively.

## Next steps

- The full plugin contract — module kinds, `RawConfig`, middleware plugins,
  testing: [writing plugins](../developer/writing-plugins.md).
- Every `hog-build` flag and the image family in depth:
  [building a custom binary](../developer/building-binaries.md).
- Skip `hog-build` entirely and own the `go build` yourself: framework mode,
  documented in [framework mode](../developer/framework-mode.md).
