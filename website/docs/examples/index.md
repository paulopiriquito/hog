# Examples

Each example on this page is a complete, copy-paste-runnable walkthrough: a
real `gateway.yaml`, the commands to run it, and the request you'd send to
see it work. They build on the vocabulary introduced in
[core concepts](../overview/concepts.md) — read that first if terms like
*terminal*, *Route*, or *RouteGroup* are new.

Start with the quick start if you're new to HOG; the rest can be read in any
order.

- **[Quick start: serve a SPA in five minutes](quickstart.md)** — build the
  `hog-static` image, serve a single-page app, and see the SPA fallback
  behavior. *Difficulty: beginner. Prerequisite: Docker and a local clone of
  the HOG repository (to build the base images).*

- **[A BFF with OIDC login](bff-oidc.md)** — a Gateway that logs users in
  through an OpenID Connect provider, holds the session in an encrypted
  cookie, and reverse-proxies an authenticated route to a backend with
  identity headers injected. *Difficulty: intermediate. Prerequisite: an
  OIDC provider (issuer, client ID/secret) and Docker.*

- **[Aggregate multiple backends](api-aggregation.md)** — an `api` terminal
  that fans out to two backends concurrently and merges their JSON under
  group keys, with partial-failure handling. *Difficulty: intermediate.
  Prerequisite: a Go toolchain (or two throwaway HTTP servers of your own).*

- **[Enforce authorization](authorization.md)** — protect routes with a
  built-in group `Policy` and an embedded Rego policy, attached to a Route
  and a RouteGroup. *Difficulty: intermediate. Prerequisite: an OIDC
  provider that issues Bearer access tokens.*

- **[Export traces to an OTLP collector](observability.md)** — turn on
  OpenTelemetry traces and metrics, point them at a collector, and read the
  trace-correlated access log. *Difficulty: intermediate. Prerequisite: an
  OTLP collector (or a Datadog Agent with OTLP intake enabled) and Docker
  Compose.*

- **[Write and build a custom plugin](custom-plugin.md)** — write a minimal
  terminal-handler plugin, declare it in the `Gateway.plugins` manifest, and
  compose a binary with `hog-build`. *Difficulty: advanced. Prerequisite:
  Docker (or a Go toolchain for local iteration).*

!!! note "Verified against the code"
    Every configuration key shown here was checked against the HOG source at
    the time of writing. See the [configuration reference](../operations/configuration.md)
    for the exhaustive list of fields per resource kind.
