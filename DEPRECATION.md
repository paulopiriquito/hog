# Deprecation notice: HOG v1

**HOG v1 is deprecated as of the HOG v2.0.0 release.**

HOG v1 is a fork of [KrakenD-CE](https://github.com/krakend/krakend-ce). HOG v2 is a
clean-room rewrite on Go 1.26 and the standard library — a native application gateway
that is **no longer a fork of KrakenD**, with its own architecture, configuration model,
and extension system.

## What this means

- **v1 receives no new features** and is maintained only as a migration reference.
- v1 source remains in this repository under [`v1/`](v1/) as an archive. Do not build
  new deployments on it.
- **New deployments should start on v2.** See the
  [v2.0.0 release notes](website/docs/releases/v2.0.0.md).

## Migrating

v2 shares no configuration format or extension model with v1, so migration is a
re-platforming rather than an in-place upgrade. Follow the
[migration guide](website/docs/releases/migrating-from-v1.md):

- KrakenD-style JSON → Kubernetes-style YAML resources (`Gateway`, `Route`,
  `RouteGroup`, `Policy`).
- Endpoints/backends → `Route` resources with `static`, `reverse-proxy`, or `api`
  handlers.
- Auth/scope validators → the built-in BFF (OIDC login + cookie session) and
  `kind: Policy` authorization (built-in rules + embedded OPA/Rego).
- Runtime `.so` plugins → compile-time Go plugins composed with `hog-build`.

## Documentation

Full documentation for v2 lives in [`website/`](website/) (an mkdocs-material site).
Build it locally with:

```sh
pip install -r website/requirements.txt
mkdocs serve -f website/mkdocs.yml
```
