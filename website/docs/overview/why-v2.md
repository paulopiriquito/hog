# Why v2

HOG v1 was a fork of KrakenD-CE. HOG v2 is a **clean-room rewrite**: no v1
code is reused — not the fork's packages, not its plugin logic, not the
KrakenD-derived core. v2 is built fresh, from the Go 1.26 standard library
up, against its own architecture. v1 remains useful only as a reference for
requirements and observed behavior (the BFF cookie flow, static serving with
auth, trace correlation) — never as a source of code to port.

## What changed

**A native core, not a framework fork.** v1 built on Lura, the KrakenD core
framework. For v2 we evaluated Lura, Caddy (as a module host), and building
natively on the standard library, and chose native. Recent Go versions moved
most of the security-sensitive primitives a gateway needs — traversal-safe
static serving (`os.Root`), a reverse proxy (`httputil.ReverseProxy`),
Fetch-metadata CSRF protection (`http.CrossOriginProtection`), structured
logging (`log/slog`), container-aware scheduling (`GOMAXPROCS`) — into the
audited standard library. That collapses the cost of owning the request
spine yourself, which was the main reason to depend on a framework in the
first place.

**Kubernetes-style YAML, not KrakenD's config format.** Every module is
configured by a resource with `apiVersion`, `kind`, `metadata`, and `spec` —
the same shape as a Kubernetes manifest, wired together with label
selectors instead of nested configuration blocks. See
[core concepts](concepts.md) for the resource model.

**Compile-time Go plugins, not `.so` files.** v1 inherited Lura's runtime
plugin model. v2 plugins are Go packages that self-register into a registry
at `init()` and are compiled into the binary alongside the built-ins — full
type safety, exact dependency versions, and zero per-request plugin-loading
overhead, at the cost of a rebuild to change the plugin set.

**Its own middleware chain.** v2's request pipeline is a fixed built-in
skeleton — recover, request-ID, access log, security, session, auth gate,
authz, projection — with two guarded slots for your code. There is no
privileged "core" code path: HOG's own features (static serving, proxying,
aggregation, authentication, authorization, observability) are built on the
same module contract a third-party plugin uses.

## Design ethos

A few decisions repeat across every v2 subsystem:

- **Standard-library-first.** External dependencies are kept deliberately
  small: an OpenID Connect (OIDC) client, a JWT library, an embedded Open
  Policy Agent (OPA) engine, a Valkey client for optional shared state, and a
  YAML parser. Where the standard library can do the job, v2 uses it.
- **Secure by default.** Static serving is traversal-resistant by
  construction. Refresh tokens never reach the browser, encrypted or not.
  Authorization fails closed: a policy evaluation error denies the request
  rather than allowing it.
- **Fail-closed, fail-fast.** Bad configuration is a boot-time error, not a
  runtime surprise. A denied policy, an expired session, or a panic all
  resolve to a safe response, never a silent pass-through.
- **One binary.** Built-in modules and your plugins compile into a single
  static artifact — no sidecars, no runtime dependencies, no external
  control plane. Any number of replicas with identical config behave as one
  coordination-free cluster.

## Where to go next

If you're running HOG v1 today, start with
[migrating from v1](../releases/migrating-from-v1.md) — it maps v1
configuration and behavior onto v2's equivalents. If you want the reasoning
behind a specific subsystem — why sessions are cookie-based, why
authorization is hard-wired to OPA, why plugins are compile-time only — read
[design choices](../design/index.md). Otherwise, continue to
[core concepts](concepts.md) or jump straight to the
[quick start](../examples/quickstart.md).
