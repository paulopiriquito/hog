# Building a custom binary

If you don't want to maintain your own Go module and build pipeline, list
your plugins in the `Gateway` resource and let the `hog-build` CLI generate
and compile the binary for you. This page distills the plugin-composition
parts of the [delivery guide](../design/delivery.md); read that page for the
full image family and deployment story.

## The plugin manifest

The `Gateway` resource's `spec.plugins` list *is* the build manifest — there's
no separate plugin file to keep in sync with your config:

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
  plugins:
    - github.com/acme/hog-geoblock@v1.4.0   # pinned — recommended, reproducible
    - github.com/acme/hog-audit             # unversioned — resolves to latest at build time
```

Each entry is `<import-path>[@version]` — the Go package whose `init()` calls
`hog.Register(...)` (see [Writing plugins](writing-plugins.md)). An empty or
absent `plugins:` list builds the vanilla binary with only the built-in
modules.

`hog-build` reads this manifest, generates a throwaway `main.go` that
blank-imports every listed plugin next to `hog.Main()` — the same shape as the
[framework mode](framework-mode.md) example, generated for you — and a `go.mod`
pinning the `hog` module to a specific source tree, then runs `go build`.

## The `hog-build` CLI

```sh
hog-build --config gateway.yaml -o ./hog
```

Flags:

- `--config <file-or-dir>` — the gateway config file or directory to read the
  manifest from. Required.
- `-o <path>` — output binary path. Default `hog`.
- `--hog-source <path>` (or `$HOG_SOURCE`) — the `hog` module source the build
  pins against via a `replace` directive, so the composed binary always
  contains *this* `hog`, not whatever `go get` would otherwise resolve.
  Required. The `hog-builder` image sets `$HOG_SOURCE` for you.
- `--replace <importpath>=<localdir>` — swap a plugin module for a local
  checkout, skipping module resolution for anything under that import path.
  Repeatable. See [local plugin development](#local-plugin-development-with-replace)
  below.
- `--tags <tags>` — passed through to `go build -tags`.
- `--go <path>` — path to the `go` binary to drive the build. Default: the
  `go` on `PATH`.
- `--keep` — keep the generated temp build directory instead of deleting it on
  exit, so you can inspect the generated `main.go`/`go.mod` after a failed
  build.

## Local plugin development with `--replace`

While iterating on a plugin you haven't published yet, point `hog-build` at
your working copy instead of a module version:

```sh
hog-build --config gateway.yaml -o ./hog \
  --replace github.com/acme/hog-geoblock=./plugins/hog-geoblock
```

The manifest entry can stay unversioned (`github.com/acme/hog-geoblock`) —
`--replace` takes priority over module resolution for anything under that
import path, including subpackages. Inside a Dockerfile, `COPY` your local
plugin source into the build context before the `RUN hog-build ...` step so
the replace target exists when the build runs.

## The two-stage Dockerfile

A custom-plugin image is a two-stage build: `hog-builder` composes the
binary, `hog-runtime` serves it. `hog-builder` (`FROM golang:1.26-alpine`)
carries the `hog` source and a compiled `hog-build`, with `$HOG_SOURCE`
pre-set; `hog-runtime` (`FROM alpine:3.21`) is the non-root, minimal serving
base every deployment ends up on:

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

See `examples/Dockerfile.custom` in the repo for the complete version,
including the `--replace` variant for local plugin sources. `hog-builder` and
`hog-runtime` are built from the repo root as the build context (`docker build
-f build/Dockerfile.builder -t hog-builder .`, then the same for
`Dockerfile.runtime`); the full image family, including the `hog-static`
convenience image, is covered in the
[delivery guide](../design/delivery.md).

Content (`/srv/web`) and config (`/etc/hog`) are always plain directories —
`COPY` them into the final stage or mount them at runtime. There's no codegen
step for your own files; only the binary itself is generated.

## Verifying the result

Once built, run the binary against your config like any other HOG binary
(`./hog --config gateway.yaml`) and confirm the routes backed by your plugins
respond as expected. See [Testing plugins](testing.md) for how to automate
this as part of your plugin's own test suite.

## Rendering config with kustomize

The `hog-builder` image includes `kustomize`, so if you manage your config
as a `kustomize` base + per-environment overlays (see
[operations: rendering config with kustomize](../operations/kustomize.md)),
you can render the overlay and feed it straight to `hog-build` in the same
build stage — no extra base image, no separate render step in CI:

```dockerfile
FROM ghcr.io/paulopiriquito/hog-builder:v2.0.0 AS build
COPY config/ ./config/
RUN kustomize build config/overlays/prod > /out/gateway.yaml
RUN hog-build --config /out/gateway.yaml -o /out/hog
```

See [operations: rendering config with kustomize](../operations/kustomize.md)
for the base/overlay layout and a complete worked example.
