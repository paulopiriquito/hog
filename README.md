# HOG

[![tests](https://github.com/paulopiriquito/hog/actions/workflows/tests.yml/badge.svg)](https://github.com/paulopiriquito/hog/actions/workflows/tests.yml)
[![release](https://img.shields.io/github/v/release/paulopiriquito/hog?sort=semver)](https://github.com/paulopiriquito/hog/releases)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**A standard-library-first Go application gateway** — a web server *and* backend-for-frontend
(BFF) / API gateway in a single static binary, configured with Kubernetes-style YAML.

One process serves your frontend, terminates the browser session, injects identity into backend
calls, aggregates APIs, and enforces authorization — with OpenID Connect login, OpenTelemetry
traces, and compile-time Go plugins. No sidecars, no runtime dependencies, no external control
plane.

📖 **Documentation:** <https://paulopiriquito.github.io/hog/>

> **HOG v2 is a clean-room rewrite on Go 1.26 and the standard library. It is no longer a fork of
> KrakenD** — it is a native application gateway with its own architecture, configuration model,
> and extension system. See [Why v2](https://paulopiriquito.github.io/hog/overview/why-v2/) and
> [Migrating from v1](https://paulopiriquito.github.io/hog/releases/migrating-from-v1/).

## Features

- **Serves your frontend** — a traversal-safe static file server with single-page-app fallback.
- **Terminates the session** — OpenID Connect login (PKCE) into an encrypted, fingerprinted cookie
  that never reaches your backends; API clients use a bearer token.
- **Bridges to your backends** — reverse-proxy a route to one upstream, or aggregate several; the
  authenticated identity is injected as `X-User-*` headers (access token forwarded only when opted in).
- **Enforces authorization** — a single `access` block with built-in group/claim rules plus embedded
  OPA/Rego policies (`kind: Policy`); additive, deny-overrides, fail-closed.
- **Is observable** — opt-in OpenTelemetry traces & metrics over OTLP, W3C propagation, and a
  trace-correlated access log.
- **Secure by default** — enforced `trustedProxies`, gateway-wide CSRF + security headers,
  `SameSite=Lax` sessions, non-root read-only-friendly images.
- **Extensible at compile time** — add Go plugins with `hog-build` (no fragile `.so` loading), or
  import HOG as a framework.

## Quick start — serve a SPA

```dockerfile
FROM ghcr.io/paulopiriquito/hog-static:latest
COPY dist/ /srv/web/
```

```sh
docker build -t my-spa . && docker run --read-only --tmpfs /tmp -p 8080:8080 my-spa
```

For a BFF with OIDC login, an API gateway with group-based authorization, and more, see the
[examples](https://paulopiriquito.github.io/hog/examples/quickstart/).

## Configuration

HOG is configured with Kubernetes-style YAML resources (`${ENV}`-expanded):

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  session: { key: ${SESSION_KEY} }
---
kind: IdP
metadata: { name: corp }
spec: { type: oidc, issuer: ${OIDC_ISSUER}, clientID: ${OIDC_CLIENT_ID}, clientSecret: ${OIDC_CLIENT_SECRET}, redirectURL: https://app.example.com/auth/callback }
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  handler: { type: reverse-proxy, upstream: http://backend:9000, stripPrefix: /api }
  access: { auth: required, authorize: [staff] }
---
kind: Policy
metadata: { name: staff }
spec: { require: { groups: [staff] } }
```

Full reference: [Configuration](https://paulopiriquito.github.io/hog/operations/configuration/).

## Container images

Published to GHCR (multi-arch: `linux/amd64` + `linux/arm64`), tagged `latest` and each release:

| Image | Purpose |
|---|---|
| `ghcr.io/paulopiriquito/hog-runtime` | Secure non-root Alpine runtime base |
| `ghcr.io/paulopiriquito/hog-static` | SPA server out of the box |
| `ghcr.io/paulopiriquito/hog-builder` | Compose a custom binary from a plugin manifest |
| `ghcr.io/paulopiriquito/hog-docs` | This documentation site |

## Build & test

```sh
make build   # go build ./cmd/hog ./cmd/hog-build
make ci      # gofmt + vet + test + race
make e2e     # docker-compose stack (dex + chromedp) end-to-end tests
make images  # build the container image family locally
make docs    # build the documentation site
```

Requires Go 1.26. `make e2e` requires Docker (or Podman).

## Extending HOG

- **Framework:** `import "github.com/paulopiriquito/hog"`, blank-import your plugin packages, call
  `hog.Main()`.
- **Plugins:** write a Go package that registers a module in its `init()`, declare it in the
  `Gateway.plugins` manifest, and compose a binary with `hog-build`.

See the [Developer guide](https://paulopiriquito.github.io/hog/developer/).

## Repository layout

`cmd/` (`hog`, `hog-build`) · `app` (assembly) · `chain` (middleware spine) · `config` · `gateway`
· `route` · `terminal` (static / reverse-proxy / api) · `session` · `idp` · `auth` · `authz` ·
`security` · `telemetry` · `registry` · `selector` · `internal/hogbuild` · `build/` (Dockerfiles) ·
`website/` (docs) · `tests/e2e/` (dex + chromedp).

## License

[Apache-2.0](LICENSE).
