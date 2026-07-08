# Delivery

HOG has no runtime plugin loading — no `.so` files, no dynamic module
fetching. Every binary is composed at compile time from a fixed set of Go
packages, and that composition happens exactly two ways, both converging on
the same static artifact.

## Two front ends, one mechanism

**As a Go framework.** Import HOG, blank-import the plugin packages you want
(their `init()` functions register themselves into HOG's module registry),
and call `hog.Main()`. This is ordinary Go: you write and build the binary
yourself, with full control over what else lives in the module.

**As a container distribution.** Most operators don't write Go at all. They
declare which plugins they need in the `Gateway` resource's `plugins:`
manifest — a list of Go import paths, optionally pinned to a version — and a
build tool, `hog-build`, composes the binary for them: it generates a small
temporary Go module that blank-imports each listed package and calls
`hog.Main()`, then runs `go build` with a static, `CGO_ENABLED=0` target.
This mirrors how Caddy's `xcaddy` composes a custom Caddy binary from a
plugin list — the pattern of generating throwaway glue code around a
manifest and letting the Go toolchain do the real work is a well-worn one,
and HOG reuses it rather than inventing a bespoke plugin loader.

Both paths produce the identical kind of artifact: one statically linked
binary with everything it needs compiled in, deployable with no runtime
dependency resolution.

## The config is the build manifest

HOG's configuration is already a set of declarative YAML resources wired
together by name and label selector. Extending that model to also declare
*which code exists* — rather than introducing a separate build
configuration file or a set of CLI flags — keeps plugin selection in the
same place as everything else an operator already reasons about: routes,
policies, and now the plugin list, all in one `Gateway` resource. A GitOps
pipeline that already diffs and reviews HOG's YAML picks up plugin changes
the same way it picks up a route change, with no separate build-config
review step.

## The image family

Container distribution ships as a three-layer Alpine image family, each
layer answering a different need:

- **`hog-builder`** — a Go build environment carrying the HOG source and a
  compiled `hog-build`, for operators who `COPY` in a `gateway.yaml` and run
  `hog-build` themselves.
- **`hog-runtime`** — the secure-by-design base every deployment runs on: a
  vanilla `hog` binary, a dedicated non-root user, and nothing baked in
  beyond what's needed to run.
- **`hog-static`** — `hog-runtime` plus a baked default configuration and an
  on-brand placeholder page, so `COPY dist/ /srv/web/` against this image is
  a complete SPA deployment with no configuration step at all.

Alpine was chosen over a distroless base for one specific reason: a fully
static, `CGO_ENABLED=0` Go binary has no libc dependency either way, so
Alpine's musl base costs nothing in compatibility — but Alpine keeps a real
shell (`busybox`'s `/bin/sh`) that distroless deliberately omits. That shell
means `docker exec ... sh` works for debugging a running container, which is
a production-debuggability trade-off HOG's runtime image accepts
deliberately: the binary still runs as a dedicated non-root user, and
read-only-root-filesystem operation is documented and supported (the
gateway is stateless), even though the base image doesn't strip itself down
to the theoretical minimum attack surface. Hardening beyond that — a
distroless or `apk`-stripped variant — is left as a possible addition
alongside, not a replacement for, the debuggable default.

## Where to go next

- [Operations: installation and images](../operations/installation.md) for
  running the shipped images.
- [Developer: building a custom binary](../developer/building-binaries.md)
  for writing and compiling in your own plugins.
