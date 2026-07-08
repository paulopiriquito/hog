# Quick start: serve a static site

Serve a plain, multi-page website — regular HTML files, no client-side
router, no build step — with HOG. This guide shows two ways to configure
it: the zero-config `hog-static` image, and an explicit `gateway.yaml` that
turns off SPA fallback so a genuinely missing page still `404`s.

!!! note "Prerequisites"
    - Docker. The images used below are pulled straight from GHCR — no
      local build or repository clone needed. See
      [Installation and images](../operations/installation.md) for the
      full image family.

## Folder structure

```text
static-site/
├── site/
│   ├── index.html
│   ├── about.html
│   └── styles.css
└── Dockerfile
```

## 1. Pull the published images

Both images used in this guide are published, multi-arch, to GHCR. Pull
them ahead of time, or just let `docker build` pull them on demand when it
hits the `FROM` line:

```sh
docker pull ghcr.io/paulopiriquito/hog-static:v2.0.0
docker pull ghcr.io/paulopiriquito/hog-runtime:v2.0.0
```

!!! success "Expected result"
    Two local images: `hog-runtime` (the vanilla binary, non-root `hog`
    user, `/etc/hog` and `/srv/web` pre-created) and `hog-static` (that plus
    a baked-in SPA config and a placeholder `index.html`). Swap `:v2.0.0`
    for `:latest` to track the newest release instead of pinning.

## 2. Write the site content

```sh
mkdir -p static-site/site && cd static-site
```

`site/styles.css`:

```css
body { font-family: sans-serif; margin: 4rem; color: #222; }
nav a { margin-right: 1rem; }
```

`site/index.html`:

```html
<!doctype html>
<html>
  <head>
    <title>Home</title>
    <link rel="stylesheet" href="/styles.css">
  </head>
  <body>
    <nav><a href="/">Home</a><a href="/about.html">About</a></nav>
    <h1>Welcome</h1>
    <p>This page is served by HOG.</p>
  </body>
</html>
```

`site/about.html`:

```html
<!doctype html>
<html>
  <head>
    <title>About</title>
    <link rel="stylesheet" href="/styles.css">
  </head>
  <body>
    <nav><a href="/">Home</a><a href="/about.html">About</a></nav>
    <h1>About</h1>
    <p>A second page, to show multi-page routing.</p>
  </body>
</html>
```

## 3. The simplest path: layer on `hog-static`

`hog-static` already bakes in `/etc/hog/gateway.yaml` with a `static` route
on `/` — there's no config to write. Create `Dockerfile`:

```dockerfile
FROM ghcr.io/paulopiriquito/hog-static:v2.0.0
COPY site/ /srv/web/
```

Build it and run it read-only:

```sh
docker build -t static-site .
docker run --read-only --tmpfs /tmp -p 8080:8080 static-site
```

!!! success "Expected result"
    A log line on stdout: `"hog listening" addr=:8080`.

```sh
curl -s http://localhost:8080/            # index.html
curl -s http://localhost:8080/about.html  # about.html
curl -s http://localhost:8080/nope        # 200 — falls back to index.html
```

The last request is the catch. `hog-static`'s baked config leaves
`spaFallback` at its default, `true` — it's meant for single-page apps.
`/nope` has no extension and no matching file, so the `static` handler
falls back to `index.html` and returns `200`. That's exactly right for a
client-routed SPA and exactly wrong for a plain multi-page site, where a
typo'd or missing URL should `404`.

## 4. The explicit path: your own `gateway.yaml`

Write `gateway.yaml` next to `site/`, and turn `spaFallback` off:

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
---
kind: Route
metadata: { name: site }
spec:
  match: /
  handler:
    type: static
    dir: /srv/web
    index: index.html
    spaFallback: false
    cacheControl: "public, max-age=3600"
  access: { auth: public }
```

Layer this on `hog-runtime` instead — the vanilla image, so you own the
whole config:

```dockerfile
FROM ghcr.io/paulopiriquito/hog-runtime:v2.0.0
COPY --chown=hog:hog site/ /srv/web/
COPY --chown=hog:hog gateway.yaml /etc/hog/gateway.yaml
```

```sh
docker build -t static-site-strict .
docker run --read-only --tmpfs /tmp -p 8080:8080 static-site-strict
```

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/about.html  # 200
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/nope        # 404
```

!!! success "Expected result"
    `/about.html` still resolves — it's a real file, and any path *with* an
    extension is never eligible for SPA fallback, flag or not. `/nope` now
    correctly returns `404` instead of silently serving the home page.

## Configuration notes

- **`dir`** is the only required field on a `static` handler — the
  directory the route serves from (`/srv/web` in both Dockerfiles above).
  Reads are contained to it (`os.OpenRoot`), so traversal outside it is
  rejected regardless of `spaFallback`.
- **`index`** defaults to `index.html`. It's also served for a directory
  request (`/` → `index.html`, `/docs/` → `docs/index.html`); HOG always
  marks the index document `Cache-Control: no-cache` so a new deploy is
  picked up immediately.
- **`spaFallback`** defaults to `true`: an extensionless path with no
  matching file falls back to the index document. Set it to `false`, as in
  step 4, for a real `404` on a multi-page site instead. A path *with* an
  extension (`.html`, `.js`, `.css`, …) is never eligible for the fallback
  either way — only extensionless paths are affected.
- **`cacheControl`** sets `Cache-Control` on every response except the
  index document.
- Both images run the `hog` binary as the non-root `hog` user (uid/gid
  `10001`) with `/etc/hog` and `/srv/web` pre-owned by it; `--chown=hog:hog`
  on `COPY` keeps that ownership when you layer your own files on.
- HOG listens on `:8080` by default (`spec.listen`). Both containers above
  run `--read-only --tmpfs /tmp` — HOG is stateless, so the root filesystem
  never needs to be writable; `/tmp` covers anything that wants a writable
  scratch dir.

## Next steps

- Add a backend and gate the site behind a login:
  [A Vue SPA and backend behind auth](quickstart-spa-backend.md).
- Protect a single route with OIDC login directly:
  [A BFF with OIDC login](bff-oidc.md).
- Full field list for `static` and every other handler: the
  [configuration reference](../operations/configuration.md).
