# Operations guide

This section is the how-to-run-it guide for HOG: installing it, writing its
configuration, wiring authentication and authorization, turning on
observability, hardening it for production, scaling it, and diagnosing
problems.

Running HOG always comes down to the same three things:

- **A binary.** `hog` (or a custom binary composed with `hog-build` — see
  [developer: building a custom binary](../developer/building-binaries.md)).
- **A config directory.** A single YAML file or a directory of `*.yaml`/`*.yml`
  files, by default `/etc/hog`, holding your `Gateway`, `Route`, and other
  resources.
- **Content, if you're serving one.** A directory of static assets (a
  single-page app build, typically `/srv/web`) that a `static` route serves.

The entrypoint takes one flag:

```sh
hog --config /etc/hog
```

`--config` accepts either a single file or a directory; when it's a
directory, files load in lexical filename order and that order becomes the
document order used to resolve plugin and policy layering. Every value in
the config supports `${ENV}` interpolation, resolved once at startup.

## In this section

- [Installation and images](installation.md) — the `hog-runtime`/`hog-static`/`hog-builder`
  image family and how to run them.
- [Configuration reference](configuration.md) — every resource kind and
  `spec` field, with minimal and annotated YAML examples.
- [Authentication](authentication.md) — wiring OIDC login, sessions, and
  protected routes.
- [Authorization](authorization.md) — writing and applying `Policy`
  resources.
- [Observability](observability.md) — enabling OpenTelemetry traces, metrics,
  and the access log.
- [Security hardening](security.md) — a checklist for running HOG safely in
  production.
- [Scaling and availability](scaling.md) — how HOG scales horizontally and
  what state (if any) it needs to share.
- [Troubleshooting](troubleshooting.md) — common failure symptoms, their
  causes, and fixes.
