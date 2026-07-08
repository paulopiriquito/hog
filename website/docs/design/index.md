# Design choices

This section explains *why* HOG is built the way it is — the reasoning and
trade-offs behind each subsystem. For step-by-step configuration, see the
[operations guide](../operations/index.md); for the request lifecycle and
configuration model, see [architecture](../architecture/index.md).

A few decisions run through every subsystem:

- **Standard-library-first.** HOG builds on Go 1.26 and `net/http` rather than
  a web framework, using dependencies only where the standard library doesn't
  reach (OIDC, JWT, OPA).
- **Secure by default, fail closed.** Sessions are encrypted end to end,
  identity headers are stripped and re-derived on every request, and both
  authentication and authorization deny when a decision can't be made safely.
- **One self-contained binary.** The same process serves static content,
  terminates the browser session, proxies and aggregates backend APIs, and
  exports telemetry — no sidecars, no external control plane.
- **Configuration as data.** Kubernetes-style YAML resources, wired together
  by name and label selector, describe both runtime behavior and — for
  container builds — which code is compiled into the binary.

## Chapters

- [Native Go, standard-library-first](native-go.md) — why v2 dropped its
  KrakenD/Lura lineage for the Go 1.26 standard library.
- [Authentication and sessions](auth-model.md) — the BFF model, the encrypted
  session cookie, and API Bearer auth.
- [Authorization](authorization.md) — the `kind: Policy` model and its
  additive, deny-overrides, fail-closed decision rules.
- [Observability](observability.md) — opt-in OpenTelemetry tracing, metrics,
  and the trace-correlated access log.
- [Delivery](delivery.md) — compile-time plugin composition and the container
  image family.
