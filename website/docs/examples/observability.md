# Export traces to an OTLP collector

HOG's telemetry is opt-in: with no `kind: Telemetry` resource, it still
propagates W3C trace context and assigns a trace ID to every request, but
exports nothing and logs at `info`. This example adds a `Telemetry` resource
that exports traces and metrics to an OTLP collector, and reads the
trace-correlated access log it produces.

!!! note "Prerequisites"
    Docker Compose, and an OTLP-compatible collector — the vanilla
    `otel/opentelemetry-collector-contrib` image below, or a Datadog Agent
    with OTLP intake enabled (same default ports: `4317` gRPC, `4318`
    HTTP).

## 1. Write `gateway.yaml`

`Telemetry` is its own top-level resource — not nested under `Gateway.spec`
— and at most one is allowed:

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
---
kind: Telemetry
metadata: { name: hog }
spec:
  logLevel: info
  service:
    name: hog-shop-gateway
    version: "1.4.2"
  otlp:
    endpoint: http://otel-collector:4318
    protocol: http/protobuf
    insecure: true
  sampling:
    ratio: 1.0
  accessLog:
    level: info
    properties: [method, path, status, duration_ms, client_ip, user_id, trace_id, span_id]
---
kind: Route
metadata: { name: spa }
spec:
  match: /
  handler: { type: static, dir: /srv/web }
  access: { auth: public }
```

- **`service.name`** is the only required field; it's applied to every
  emitted span/metric as the OTel resource `service.name`, and to every
  access-log line via a base `service` attribute.
- **`otlp.endpoint`** is what actually turns exporting on — omit it and
  `logLevel`/`accessLog`/trace-context propagation still work, but nothing
  leaves the process. It must be an `http://` or `https://` URL.
  `protocol` is `http/protobuf` (default) or `grpc`; `insecure: true` skips
  TLS for a plaintext local collector.
- **`sampling.ratio`** is a parent-based head sample rate from `0` to `1`;
  `1.0` here traces everything, appropriate for a demo, not necessarily
  production traffic.
- **`accessLog.properties`** replaces the default set entirely when given —
  it's an allowlist of exactly these fifteen names: `method`, `path`,
  `route`, `query`, `status`, `duration_ms`, `bytes_out`, `client_ip`,
  `host`, `protocol`, `user_id`, `session_id`, `request_id`, `trace_id`,
  `span_id`. `accessLog.headers` (field name → request header) and
  `accessLog.fields` (static custom fields) can add more; credential
  headers (`Authorization`, `Cookie`, `Proxy-Authorization`, `Set-Cookie`)
  are rejected at startup if you try to capture them.

## 2. Run HOG next to a collector

```yaml
# docker-compose.yaml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.110.0
    command: ["--config=/etc/otel-collector-config.yaml"]
    volumes:
      - ./otel-collector-config.yaml:/etc/otel-collector-config.yaml:ro
    ports:
      - "4318:4318"

  hog:
    image: hog-runtime
    volumes:
      - ./gateway.yaml:/etc/hog/gateway.yaml:ro
      - ./dist:/srv/web:ro
    ports:
      - "8080:8080"
    depends_on:
      - otel-collector
```

`otlp.endpoint: http://otel-collector:4318` in `gateway.yaml` resolves via
Compose's service DNS, so the two containers only need to share this
network. Build `hog-runtime` locally first, as in the
[quick start](quickstart.md), then:

```sh
docker compose up
```

## 3. Generate a request and read the access log

```sh
curl -s http://localhost:8080/ > /dev/null
```

!!! success "Result"
    A JSON line on HOG's stdout, one per request:

    ```json
    {"time":"2026-07-08T12:00:00Z","level":"INFO","msg":"access","service":"hog-shop-gateway","method":"GET","path":"/","status":200,"duration_ms":4,"client_ip":"172.18.0.1","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
    ```

    `user_id` is present only when a request carries a resolved principal
    (see [A BFF with OIDC login](bff-oidc.md)); it's silently omitted
    otherwise rather than logged empty. `trace_id`/`span_id` correlate this
    line to the span you'll see in your collector/backend — search either
    ID and land on the same request from both directions.

## Next steps

- Full `Telemetry` field list and defaults: the
  [configuration reference](../operations/configuration.md) and
  [observability](../operations/observability.md).
- Why these particular defaults (parent-based sampling, opt-in export,
  query redaction): [design: observability](../design/observability.md).
