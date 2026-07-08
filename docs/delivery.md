# Delivering HOG

HOG ships two ways. Both compile down to the same thing: a single static Go binary
made of the built-in modules plus whatever plugins you declare. Pick whichever fits
how your team works.

## Mode 1: import it as a framework

If you already write Go, `hog` is just a package:

```go
package main

import (
	"github.com/paulopiriquito/hog"

	_ "github.com/acme/hog-geoblock" // blank-import each plugin; its init() registers it
	_ "github.com/acme/hog-audit"
)

func main() { hog.Main() }
```

`hog.Main()` parses `--config` (default `/etc/hog`), loads your `Gateway` /
`Route` / etc. resources, and serves until it's signaled to stop. `go build` the
result like any other Go program. There's no build tool involved — you're in full
control of the module.

## Mode 2: declare plugins in config, let `hog-build` compose the binary

If you don't want a Go build pipeline of your own, list your plugins in the
`Gateway` resource and let `hog-build` generate + compile the binary for you:

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
  plugins:
    - github.com/acme/hog-geoblock@v1.4.0   # pinned — recommended, reproducible
    - github.com/acme/hog-audit             # unversioned — resolves to latest at build time
```

Each entry is `<import-path>[@version]` — the package whose `init()` calls
`hog.Register(...)`. Then:

```sh
hog-build --config gateway.yaml -o ./hog
```

`hog-build` reads the manifest, generates a throwaway `main.go` that blank-imports
every listed plugin next to `hog.Main()`, and runs `go build`. The config *is* the
build manifest — no separate plugin list to keep in sync. An empty (or absent)
`plugins:` list just builds the vanilla binary.

`hog-build --config <file-or-dir> -o <output>` also takes:

- `--replace <importpath>=<localdir>` — swap a module for a local checkout (repeatable; see below).
- `--tags <tags>` — passed through to `go build -tags`.
- `--go <path>` — use a specific `go` binary (default: `PATH`'s `go`).
- `--hog-source <path>` (or `$HOG_SOURCE`) — the `hog` module source the build pins
  against, so the composed binary always contains *this* `hog`, not whatever
  `go get` would otherwise resolve. Required. The `hog-builder` image sets it for you.
- `--keep` — keep the temp build dir instead of deleting it on exit, so you can
  inspect the generated `main.go`/`go.mod` after a failed build.

## The image family

Three layered Alpine images, each `FROM` the last, so you only build what you need:

```
hog-builder  (FROM golang:1.26-alpine)
      │  hog source + compiled `hog-build`
      ▼
hog-runtime  (FROM alpine:3.21)
      │  vanilla `hog` binary, non-root user, entrypoint
      ▼
hog-static   (FROM hog-runtime)
         baked SPA config + placeholder page
```

Build all three with the repo root as the build context. `hog-builder` and
`hog-runtime` `COPY . .` to compile from the hog source; `hog-static` only copies
the two files under `build/static/` onto `hog-runtime`, so it must be built after
`hog-runtime` is tagged:

```sh
docker build -f build/Dockerfile.builder -t hog-builder .
docker build -f build/Dockerfile.runtime -t hog-runtime .   # hog-static's FROM depends on this tag
docker build -f build/Dockerfile.static  -t hog-static  .
```

**`hog-runtime`** is the base every deployment ends up on: a non-root `hog`
user (uid/gid `10001`), `ca-certificates` + `tzdata`, the busybox shell kept for
`docker exec ... sh` debugging, `/etc/hog` and `/srv/web` pre-created and
owned by `hog`, and `ENTRYPOINT ["/usr/local/bin/hog", "--config", "/etc/hog"]`.

**`hog-static`** is `hog-runtime` with a default SPA config and placeholder page
baked in (`/etc/hog/gateway.yaml`, `/srv/web/index.html`) — pull it, `COPY
dist/ /srv/web/`, and you have an instant, secured static server:

```dockerfile
FROM hog-static
COPY dist/ /srv/web/
```

**`hog-builder`** is only needed to *compose* a custom binary (Mode 2). A
custom-plugin image is a two-stage build — `hog-builder` composes, `hog-runtime`
serves:

```dockerfile
# syntax=docker/dockerfile:1
FROM hog-builder AS build
COPY plugins/ ./plugins/
COPY gateway.yaml .
RUN hog-build --config gateway.yaml -o /out/hog

FROM hog-runtime
COPY --from=build /out/hog /usr/local/bin/hog
COPY --chown=hog:hog config/ /etc/hog/
COPY --chown=hog:hog web/    /srv/web/
```

See `examples/Dockerfile.custom` for the full version. Content (`/srv/web`) and
config (`/etc/hog`) are always plain directories — `COPY` them in at build time or
mount them at runtime; there's no codegen step for your own files.

### Local plugin development with `--replace`

While iterating on a plugin you haven't published yet, point `hog-build` at your
working copy instead of a module version:

```sh
hog-build --config gateway.yaml -o ./hog \
  --replace github.com/acme/hog-geoblock=./plugins/hog-geoblock
```

The manifest entry can stay unversioned (`github.com/acme/hog-geoblock`) —
`--replace` takes priority and skips module resolution for anything under that
import path. Inside a Dockerfile, `COPY` your local plugin source in before the
`RUN hog-build ...` step so the replace target exists in the build context.

### Running read-only, as a non-root user

`hog` is stateless, so run the container with a read-only root filesystem — the
`hog-runtime`/`hog-static` images already run as the non-root `hog` user by
default:

```sh
docker run --read-only --tmpfs /tmp -p 8080:8080 hog-static
```

`--tmpfs /tmp` covers anything that expects a writable scratch dir (none of the
built-ins need it today, but it's cheap insurance). Mount your config and content
read-only too if you're not baking them into the image.

## Deferred

- **Baking content/config into the binary** (`embed.FS`) — today they're always
  directories (`COPY`/mount). A single-artifact binary is a future optimization,
  not required to ship.
- **Publishing the image family** to a registry (multi-arch builds, signing, SBOM)
  is release/CI infrastructure, out of scope here — this repo ships the
  Dockerfiles, not published images.
