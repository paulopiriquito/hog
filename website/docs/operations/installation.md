# Installation and images

HOG ships as three layered Alpine images. Each is `FROM` the previous one, so
you only build the layer you need.

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

- **`hog-runtime`** is the base every deployment ends up on: the vanilla
  `hog` binary, a non-root `hog` user (uid/gid `10001`), `ca-certificates` +
  `tzdata`, and `/etc/hog` + `/srv/web` pre-created and owned by `hog`.
- **`hog-static`** is `hog-runtime` with a default single-page-app (SPA)
  config and placeholder page baked in — an instant static server.
- **`hog-builder`** is only needed to *compose* a custom binary with plugins
  compiled in. See
  [developer: building a custom binary](../developer/building-binaries.md).

Build all three from the repository root (the build context each Dockerfile
expects):

```sh
docker build -f build/Dockerfile.builder -t hog-builder .
docker build -f build/Dockerfile.runtime -t hog-runtime .   # hog-static depends on this tag
docker build -f build/Dockerfile.static  -t hog-static  .
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
  hog-runtime
```

## The SPA out of the box: `hog-static`

`hog-static` already has `/etc/hog/gateway.yaml` (a `Gateway` + a public
`static` route serving `/srv/web`) and a placeholder `/srv/web/index.html`
baked in. Replace the placeholder with your build output and you have a
secured static server with no config to write:

```dockerfile
FROM hog-static
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
docker run --read-only --tmpfs /tmp -p 8080:8080 hog-static
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
