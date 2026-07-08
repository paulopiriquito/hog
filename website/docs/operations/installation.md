# Installation and images

HOG ships as four Alpine images, published to
[GHCR](https://ghcr.io) as multi-arch builds (`linux/amd64` +
`linux/arm64`), tagged both `latest` and with each release:

```sh
docker pull ghcr.io/paulopiriquito/hog-runtime:v2.0.0
docker pull ghcr.io/paulopiriquito/hog-static:v2.0.0
docker pull ghcr.io/paulopiriquito/hog-builder:v2.0.0
docker pull ghcr.io/paulopiriquito/hog-docs:v2.0.0
```

Swap `:v2.0.0` for `:latest` to track the newest release instead of pinning.
You rarely need to `docker pull` explicitly — referencing an image in a
`FROM` line is enough; `docker build` pulls it on demand.

| Image | Purpose |
| --- | --- |
| `ghcr.io/paulopiriquito/hog-runtime` | The base every deployment ends up on: the vanilla `hog` binary, a non-root `hog` user (uid/gid `10001`), `ca-certificates` + `tzdata`, and `/etc/hog` + `/srv/web` pre-created and owned by `hog`. |
| `ghcr.io/paulopiriquito/hog-static` | `hog-runtime` with a default single-page-app (SPA) config and placeholder page baked in — an instant static server. |
| `ghcr.io/paulopiriquito/hog-builder` | A Go build environment carrying the HOG source, for composing a custom binary with plugins compiled in. See [developer: building a custom binary](../developer/building-binaries.md). |
| `ghcr.io/paulopiriquito/hog-docs` | This documentation site, baked onto `hog-static`. |

The four are layered — `hog-static` is `FROM hog-runtime`, `hog-docs` is
`FROM hog-static` — but each is published as a complete, independently
pullable image. You never need to build or pull the layer underneath the
one you use.

```
hog-builder  (FROM golang:1.26-alpine)
      │  hog source + compiled `hog-build`
      ▼
hog-runtime  (FROM alpine:3.21)
      │  vanilla `hog` binary, non-root user, entrypoint
      ▼
hog-static   (FROM hog-runtime)
      │  baked SPA config + placeholder page
      ▼
hog-docs     (FROM hog-static)
         this documentation site
```

## Running the vanilla runtime

`hog-runtime`'s entrypoint is fixed:

```dockerfile
ENTRYPOINT ["/usr/local/bin/hog", "--config", "/etc/hog"]
```

Mount your config and content directories and publish the listen port (the
`Gateway` resource's `listen`, `:8080` by default, matching the image's
`EXPOSE 8080`):

```sh
docker run \
  -p 8080:8080 \
  -v "$PWD/config:/etc/hog:ro" \
  -v "$PWD/dist:/srv/web:ro" \
  ghcr.io/paulopiriquito/hog-runtime:v2.0.0
```

## The SPA out of the box: `hog-static`

`hog-static` already has `/etc/hog/gateway.yaml` (a `Gateway` + a public
`static` route serving `/srv/web`) and a placeholder `/srv/web/index.html`
baked in. Replace the placeholder with your build output and you have a
secured static server with no config to write:

```dockerfile
FROM ghcr.io/paulopiriquito/hog-static:v2.0.0
COPY dist/ /srv/web/
```

```sh
docker build -t my-spa .
docker run -p 8080:8080 my-spa
```

To serve a different directory or add routes (a BFF, an API aggregate,
authentication), overlay your own `Gateway`/`Route` resources instead of
relying on the baked-in default — see the
[configuration reference](configuration.md).

## Running read-only, as the non-root user

HOG is stateless, so nothing it does at runtime requires a writable
filesystem. `hog-runtime` and `hog-static` already run as the non-root `hog`
user by default; run the container with a read-only root filesystem too:

```sh
docker run --read-only --tmpfs /tmp -p 8080:8080 ghcr.io/paulopiriquito/hog-static:v2.0.0
```

`--tmpfs /tmp` covers any scratch-directory expectations (none of the
built-in modules need one today, but it costs nothing to allow). Mount your
config and content read-only as well if you aren't baking them into the
image. See [security hardening](security.md) for the full checklist.

!!! tip "Custom plugins"
    If you need Go plugins compiled into the binary — a custom
    `TerminalHandler`, `RequestPlugin`, `IdP`, or `StateProvider` — see
    [developer: building a custom binary](../developer/building-binaries.md)
    for the two-stage `hog-builder` → `hog-runtime` build.

## Building from source

Most users should pull the published images above. Contributors — or
anyone who needs an image built from a specific commit rather than a
tagged release — can build the whole family locally from the repository
root with:

```sh
make images   # REGISTRY=ghcr.io/paulopiriquito TAG=dev by default
```

which builds `hog-builder`, then `hog-runtime`, then layers `hog-static` on
top of it and `hog-docs` on top of that — the same chain and the same
Dockerfiles (`build/Dockerfile.{builder,runtime,static}`,
`website/Dockerfile`) the published GHCR images are built from. Override
`REGISTRY`/`TAG` to build under different names, e.g.
`make images REGISTRY=myrepo TAG=local`.
