---
hide:
  - navigation
  - toc
---

# HOG

**A standard-library-first Go application gateway.** HOG serves your frontend and
acts as its backend-for-frontend (BFF) and API gateway — in a single static
binary, configured with Kubernetes-style YAML.

<p class="hog-tagline">
One process terminates the browser session, serves the SPA, injects identity into
backend calls, aggregates APIs, and enforces authorization — with OpenID Connect
login, OpenTelemetry traces, and compile-time Go plugins. No sidecars, no runtime
dependencies, no external control plane.
</p>

---

## Start here

<div class="grid cards" markdown>

-   :material-rocket-launch: **Quick start**

    ---

    Serve a single-page app behind HOG in five minutes with the `hog-static`
    image.

    [:octicons-arrow-right-24: Quick start](examples/quickstart.md)

-   :material-map: **Overview**

    ---

    What HOG is, the core concepts, and where it fits in your stack.

    [:octicons-arrow-right-24: Overview](overview/index.md)

-   :material-sitemap: **Architecture**

    ---

    The request lifecycle, the configuration model, and the deployment topology.

    [:octicons-arrow-right-24: Architecture](architecture/index.md)

-   :material-cog: **Operations guide**

    ---

    Install, configure, secure, and run HOG in production.

    [:octicons-arrow-right-24: Operations](operations/index.md)

-   :material-code-braces: **Developer guide**

    ---

    Extend HOG with Go plugins, or embed it as a framework.

    [:octicons-arrow-right-24: Developer guide](developer/index.md)

-   :material-lightbulb-on: **Design choices**

    ---

    Why HOG is built the way it is — the trade-offs behind each subsystem.

    [:octicons-arrow-right-24: Design choices](design/index.md)

</div>

---

## What HOG does

- **Serves your frontend.** A traversal-safe static file server with single-page
  application fallback.
- **Terminates the session.** Browser login through any OpenID Connect provider;
  the session lives in an encrypted cookie, never exposed to your backends.
- **Bridges to your backends.** Reverse-proxy and multi-backend API aggregation
  terminals inject the authenticated identity as headers — and, in BFF mode, the
  access token as a bearer credential.
- **Enforces authorization.** Built-in group and claim rules, plus embedded
  OPA/Rego policies, applied per route or route group. Additive, deny-overrides,
  fail-closed.
- **Is observable.** Opt-in OpenTelemetry traces and metrics over OTLP, W3C
  context propagation, and a trace-correlated access log.
- **Ships as one binary.** Compile your Go plugins in at build time with
  `hog-build`, or run the batteries-included images.

## Why v2

HOG v2 is a clean-room rewrite on Go 1.26 and the standard library. It is **no
longer a fork of KrakenD** — it is a native application gateway with its own
architecture, configuration model, and extension system.

[:octicons-arrow-right-24: Why v2](overview/why-v2.md) ·
[:octicons-arrow-right-24: Migrating from v1](releases/migrating-from-v1.md)
