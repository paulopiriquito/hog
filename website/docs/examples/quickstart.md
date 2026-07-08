# Quick start

These three guides take you from a plain static site to a full API gateway
with login and per-group authorization, each one copy-paste-runnable and a
little more advanced than the last. Work through them in order if HOG is new
to you, or jump straight to the one that matches what you're building.

!!! note "Prerequisites"
    - **Docker**, for all three.
    - Guides 2 and 3 also need an **OIDC provider** (Keycloak, Auth0, Okta,
      Dex, …) that supports discovery: an issuer URL, a client ID, and a
      client secret.
    - None of the three need a published HOG image — the
      `hog-runtime`/`hog-static` image family isn't on a registry yet, so
      each guide builds them locally from a clone of the repository. See
      [Delivering HOG](https://github.com/paulopiriquito/hog/blob/v2/docs/delivery.md)
      for why.

## The three guides

1. **[Serve a static site](quickstart-static.md)** — copy plain HTML into an
   image and serve it. No auth, no backend: the fastest way to see HOG serve
   real content. *Beginner.*
2. **[A Vue SPA and backend behind auth](quickstart-spa-backend.md)** —
   build a Vite SPA, serve it through HOG, reverse-proxy `/api` to a
   backend, and gate both behind an OIDC login. *Intermediate.*
3. **[An API gateway with authorization](quickstart-api-gateway.md)** —
   authenticate callers and then gate individual routes by the caller's
   group membership and a Rego rule. *Intermediate.*

## Beyond the quick starts

Each quick start builds one deployable shape end to end. For a deeper look
at a single feature, see:

- **[A BFF with OIDC login](bff-oidc.md)** — the login flow and identity
  header injection, in isolation.
- **[Aggregate multiple backends](api-aggregation.md)** — fan a single
  request out to several backends and merge the results.
- **[Enforce authorization](authorization.md)** — built-in group policies
  and embedded Rego, attached to routes and route groups.
- **[Export traces to an OTLP collector](observability.md)** — traces,
  metrics, and a trace-correlated access log.
- **[Write and build a custom plugin](custom-plugin.md)** — extend HOG with
  your own terminal handler and compose a binary with `hog-build`.

For the exhaustive field list behind every example on this site, see the
[configuration reference](../operations/configuration.md).
