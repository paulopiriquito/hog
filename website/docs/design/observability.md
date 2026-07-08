# Observability

HOG runs as a fleet of stateless, coordination-free replicas behind a load
balancer. Operators need to see, per request, how long HOG took, which route
handled it, which backend was slow or failing, and how a log line ties back
to a trace — including traces that started upstream, at the load balancer or
another service. Observability in HOG is built around OpenTelemetry to
deliver that without asking every deployment to pay for it.

## Traces and metrics, opt-in

Distributed tracing and metrics are exported over OTLP, but only when an
operator configures an export endpoint. Without one, HOG still assigns every
request a valid trace and span ID — so propagation and log correlation work
out of the box — but nothing is exported, and no connection to a collector is
ever opened. Metrics are meaningless without a place to send them, so the
meter provider stays a no-op until export is configured.

This is a deliberate default: a gateway that adds network calls and CPU
overhead just by starting up is a bad default for a request-path component.
Making export opt-in means the zero-configuration cost is genuinely zero, and
a down or unreachable collector never affects request handling — export
failures are logged, not surfaced to the client.

## W3C trace context, always on

Trace propagation — reading an inbound `traceparent` header and continuing
that trace, or minting a fresh one when there isn't one, then attaching
`traceparent` to every outbound backend call — runs unconditionally,
independent of whether export is configured. It's cheap, local, and it's
what makes the trace and span IDs in the access log meaningful even when
nothing is exported.

HOG speaks the standard W3C `traceparent`/`tracestate` and `baggage` formats
rather than a vendor-specific propagation header. This is enough for
interoperability with Datadog without any Datadog-specific dependency:
Datadog's tracers default to emitting *and* accepting W3C `traceparent`
alongside their native format, precisely for cross-vendor interoperability.
A trace that starts at a Datadog-instrumented load balancer and passes
through HOG to a Datadog-instrumented backend stays a single, continuous
trace, with no vendor-aware code in HOG at all.

## A trace-correlated access log

The access log is a configurable, structured `slog` line per request rather
than a fixed format. Operators choose which built-in properties appear
(method, path, matched route, status, duration, client IP, and more), which
request headers to capture as fields, and static fields whose values can
come from `${ENV}` — useful for stamping deployment or region metadata onto
every line. Every line carries `trace_id` and `span_id`, so a log line and
a trace are one lookup apart.

Opting out is by log level, not a separate toggle: raising the configured
log level above the access log's level suppresses it, at the cost of just a
level check per request. When a delegated session store is configured, each
line also carries a `session_id` — not the raw session identifier (which is
a bearer credential and is never logged) but a one-way hash of it, enough to
correlate requests belonging to the same session without exposing anything
an attacker could replay.

## Credential hygiene

Nothing that could authenticate a request or a user reaches a span or a log
line. Access tokens, refresh tokens, and Bearer credentials are never
logged, whatever headers or properties are configured to be captured. Query
strings are redacted before logging: known-sensitive parameter values are
replaced rather than logged verbatim, since query parameters are a common
place for tokens and keys to leak into logs.

## Configuration

Telemetry is off — or rather, non-exporting — by default. Enabling export,
setting the service name, or tuning sampling and the access log all happen
through a dedicated `Telemetry` resource in the same YAML configuration
model as every other HOG resource; see
[operations: observability](../operations/observability.md) for the
configuration reference.
