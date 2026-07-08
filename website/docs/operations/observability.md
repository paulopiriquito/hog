# Configure observability

HOG's observability is a single `kind: Telemetry` resource: OpenTelemetry
(OTLP) traces and metrics, W3C trace-context propagation, and a
configurable, trace-correlated structured access log. It's **opt-in** —
omit the resource and HOG runs with a default access log and no exporters.

## Turn it on

```yaml
kind: Telemetry
metadata: { name: telemetry }
spec:
  service:
    name: my-gateway
  otlp:
    endpoint: http://otel-collector:4318
```

`service.name` is the only required field once you add a `Telemetry`
resource at all. Everything else defaults.

| Field | Type | Default | Description |
|---|---|---|---|
| `logLevel` | string | `info` | Level for the application logger (`debug`\|`info`\|`warn`\|`error`). |
| `service.name` | string | — | **Required** (once `Telemetry` is present). Sets the OTel resource `service.name`. |
| `service.version` | string | — | Sets `service.version`. |
| `otlp.endpoint` | string | — | An `http(s)://host:port` collector URL, e.g. `http://collector:4318`. **Unset = no exporters** — traces/metrics are still generated internally (for the access log's `trace_id`/`span_id`) but never sent anywhere. |
| `otlp.protocol` | string | `http/protobuf` | `http/protobuf` or `grpc`. |
| `otlp.headers` | map[string]string | — | Extra headers sent with every export call (e.g. an API key). |
| `otlp.insecure` | bool | `false` | Skip TLS on the OTLP connection. |
| `otlp.timeout` | duration string | exporter default | Per-export timeout. |
| `sampling.ratio` | float | `1.0` | Head, parent-based `TraceIDRatioBased` sampling (`0`..`1`). Ignored (no sampling — no exporter) when `otlp.endpoint` is unset. |
| `accessLog.level` | string | `info` | Level the access-log line itself is emitted at. |
| `accessLog.properties` | []string | see below | Which built-in fields to include. An explicit list **replaces** the default set entirely. |
| `accessLog.headers` | map[string]string | — | `field name → request header` to copy into the log line. |
| `accessLog.fields` | map[string]string | — | Static custom fields (`${ENV}`-resolved at load, e.g. a deploy/region tag). |

Default `accessLog.properties`:

```
method, path, status, duration_ms, client_ip, request_id, trace_id, span_id
```

Full set of allowed properties: `method`, `path`, `route`, `query`,
`status`, `duration_ms`, `bytes_out`, `client_ip`, `host`, `protocol`,
`user_id`, `session_id`, `request_id`, `trace_id`, `span_id`. An unknown
name in `properties` is a startup error.

```yaml
kind: Telemetry
metadata: { name: telemetry }
spec:
  logLevel: info
  service:
    name: my-gateway
    version: "1.4.0"
  otlp:
    endpoint: ${OTLP_ENDPOINT}
    protocol: grpc
    headers:
      Authorization: "Bearer ${OTLP_API_KEY}"
  sampling:
    ratio: 0.2
  accessLog:
    level: info
    properties: [method, path, status, duration_ms, client_ip, user_id, session_id, trace_id]
    fields:
      deploy_env: production
```

## What's emitted

- **Traces.** Every request is wrapped in an OTel server span
  (`otelhttp`), and every outbound backend call (reverse-proxy or `api`
  aggregation) is instrumented as a client span, so a single trace covers
  the whole request → backend(s) round trip. HOG continues an inbound
  `traceparent` rather than starting a fresh linked trace — it expects to
  sit behind a load balancer or another instrumented service, not to be the
  root of the trace.
- **Metrics.** A metric provider is always installed; a meter with no
  configured OTLP endpoint simply drops recordings rather than exporting
  them.
- **The access log.** One structured (`slog`) `"access"` line per request,
  correlated with the request's `trace_id`/`span_id` when tracing is
  active, at `accessLog.level`. Raising the application logger's level above
  `accessLog.level` suppresses it. It streams correctly through SSE/chunked
  responses and WebSocket upgrades.
- **Authorization decisions.** A policy deny is recorded as an
  `authz.deny` span event (policy name + reason) in addition to being
  logged — see [authorization](authorization.md).

## Propagation

HOG always installs the W3C `TraceContext` + `Baggage` propagator,
independent of whether `otlp.endpoint` is set — so trace headers pass
through even when you aren't exporting anything yet.

## Credentials are never logged or traced

- The access log's `query` property redacts known-sensitive parameter names
  (`code`, `state`, `token`, `access_token`, `id_token`, `refresh_token`,
  `api_key`, `apikey`, `client_secret`, `password`, `assertion`) to
  `REDACTED`, and fails closed to an empty string if the query string can't
  be parsed at all — it never echoes an unparseable raw query that might
  carry a secret.
- `accessLog.headers` **cannot** capture `Authorization`, `Cookie`,
  `Proxy-Authorization`, or `Set-Cookie` — configuring one of these is a
  startup error.
- Access tokens, refresh tokens, and session cookie values are never
  included in `input` to authorization policies, in log lines, or in span
  attributes anywhere in the codebase.

## Point it at a collector

Any OTLP-compatible collector works — an OpenTelemetry Collector, or a
vendor endpoint that speaks OTLP/HTTP or OTLP/gRPC directly:

```sh
docker run -p 4318:4318 -p 4317:4317 \
  otel/opentelemetry-collector:latest
```

```yaml
spec:
  otlp:
    endpoint: http://otel-collector:4318   # http/protobuf; use :4317 + protocol: grpc for gRPC
```

A down or unreachable collector is not a startup error — export is a
lazy-connect, best-effort background process, not a boot-time dependency.
