# Structured Observability with OTEL/Datadog Trace Correlation

This document describes how to configure structured logging with trace correlation for the HOG gateway.

## Configuration Options

### `telemetry/logging` Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `format` | string | `""` | Log format: `"json"`, `"logstash"`, `"custom"`, or empty for text |
| `access_log` | bool | `true` | Enable/disable access logging for HTTP requests |
| `skip_paths` | []string | `[]` | Paths to exclude from access logging (e.g., `["/__health"]`) |
| `trace_format` | string | `"otel"` | Trace ID format in logs/propagation: `"otel"` (`trace_id`/`span_id`), `"datadog"` (`dd.trace_id`/`dd.span_id`), or `"both"` |
| `tags` | object | `{}` | Extra fields added to every log line. A value of the form `$VAR` or `${VAR}` is resolved from the environment at startup (see below) |

### Environment-sourced log fields

`tags` is a `field_name → value` map whose entries become extra fields on every
log line (gateway and plugin). A value written as `$VAR` or `${VAR}` is replaced
with the value of that environment variable at startup; anything else is used
literally. Unset variables resolve to an empty string.

```json
"tags": {
  "service": "gateway",          // literal
  "version": "$HOG_VERSION",     // from env
  "region":  "$AWS_REGION",      // from env
  "instance": "$HOSTNAME"        // e.g. the pod/container name
}
```

This is handy in Kubernetes for stamping every log with `pod`, `node`, or
`version` pulled from Downward-API env vars, without rebuilding config per
environment.

### Trace Format Details

- **`otel`**: Uses W3C trace context format
  - Outputs: `trace_id` (32-char hex), `span_id` (16-char hex)
  - Propagates: `traceparent`, `tracestate` headers

- **`datadog`**: Uses Datadog format
  - Outputs: `dd.trace_id` (decimal), `dd.span_id` (decimal)
  - Propagates: `x-datadog-trace-id`, `x-datadog-parent-id`, `x-datadog-sampling-priority` headers

- **`both`**: Outputs and propagates both formats

## Sample Configurations

### Local Development (OTEL + Jaeger)

```json
{
  "extra_config": {
    "telemetry/logging": {
      "level": "DEBUG",
      "format": "json",
      "access_log": true,
      "skip_paths": ["/__health", "/__debug"],
      "trace_format": "otel",
      "stdout": true,
      "tags": {
        "env": "dev",
        "service": "gateway"
      }
    },
    "telemetry/opentelemetry": {
      "service_name": "hog-gateway",
      "service_version": "v1.0.0",
      "skip_paths": ["/__health"],
      "exporters": {
        "otlp": [{
          "name": "local-jaeger",
          "host": "otel-collector",
          "port": 4317,
          "use_http": false
        }]
      }
    }
  }
}
```

### Production (native Datadog on Kubernetes)

In Kubernetes the Datadog Agent runs as a per-node DaemonSet, so hog ships to the
**node-local Agent** (no OpenObserve, no separate collector). The Agent's OTLP
ingest receives the traces/metrics and forwards them to Datadog APM natively, and
the Agent's log pipeline tails the pod's stdout JSON and correlates it to traces
by `dd.trace_id`.

```json
{
  "extra_config": {
    "telemetry/logging": {
      "level": "INFO",
      "format": "json",
      "access_log": true,
      "skip_paths": ["/__health"],
      "trace_format": "datadog",
      "stdout": true,
      "tags": {
        "service": "gateway",
        "version": "$HOG_VERSION",
        "pod":     "$HOSTNAME",
        "node":    "$DD_AGENT_HOST"
      }
    },
    "telemetry/opentelemetry": {
      "service_name": "hog-gateway",
      "skip_paths": ["/__health"],
      "exporters": {
        "otlp": [{
          "name": "datadog-agent",
          "host": "{{ env \"DD_AGENT_HOST\" }}",
          "port": 4317,
          "use_http": false
        }]
      }
    }
  }
}
```

**Wiring it up:**

1. **Node-local Agent address** — expose the node IP to the pod via the Downward
   API and point the OTLP exporter at it. The `{{ env "DD_AGENT_HOST" }}` host
   above requires KrakenD [Flexible Configuration](https://www.krakend.io/docs/configuration/flexible-config/)
   (`FC_ENABLE=1`); alternatively set `OTEL_EXPORTER_OTLP_ENDPOINT=http://$(DD_AGENT_HOST):4318`.

   ```yaml
   env:
     - name: DD_AGENT_HOST
       valueFrom: { fieldRef: { fieldPath: status.hostIP } }
     - name: HOSTNAME
       valueFrom: { fieldRef: { fieldPath: metadata.name } }
     - name: HOG_VERSION
       value: "1.2.0"
   ```

2. **Enable OTLP on the Agent** — `DD_OTLP_CONFIG_RECEIVER_PROTOCOLS_GRPC_ENDPOINT=0.0.0.0:4317`
   (and the HTTP one for `:4318`).

3. **`trace_format: "datadog"`** (or `"both"`) makes every log line carry
   `dd.trace_id` / `dd.span_id`. The Agent's container log collection picks up the
   pod's stdout and auto-correlates logs ↔ traces — no log shipper needed.

> Use `trace_format: "both"` if you want the same build to also feed an
> OTEL/W3C backend (e.g. OpenObserve locally) while emitting Datadog IDs.

## Environment Variables

For the authenticator plugin, set `TRACE_FORMAT` to match your logging configuration:

```bash
# Local development
TRACE_FORMAT=otel

# Production with Datadog
TRACE_FORMAT=datadog
```

## Docker Compose Setup (Local Development)

The `tests/local-stack/docker-compose.yaml` ships an OTEL-native observability
pipeline:

- **otel-collector**: OpenTelemetry Collector (contrib) — receives OTLP from hog
  on `:4317` (gRPC) / `:4318` (HTTP) and forwards to OpenObserve. It adds the
  Basic-auth header OpenObserve's ingest requires, which KrakenD's OTLP exporter
  cannot set itself.
- **openobserve**: single-binary, OTEL-native backend **and** UI at
  http://localhost:5080 (login `root@example.com` / `Complexpass123`). Stores
  traces (stream `default`) and metrics (`krakend_*`, `http_server_*`) in one place.

hog exports OTLP to the collector via `OTEL_EXPORTER_OTLP_ENDPOINT` (env) and the
`telemetry/opentelemetry` OTLP exporter (`krakend.json`). Structured logs stay on
hog's stdout; pivot to a trace by searching its `trace_id` in OpenObserve.

## Access Log Output Examples

### JSON Format (OTEL)

```json
{
  "timestamp": "2026-01-12T15:08:14.123Z",
  "level": "INFO",
  "module": "ACCESS",
  "method": "GET",
  "path": "/api/users",
  "status": 200,
  "latency_ms": 12.5,
  "client_ip": "10.0.0.1",
  "user_agent": "Mozilla/5.0...",
  "host": "api.example.com",
  "trace_id": "abc123def456...",
  "span_id": "1234567890abcdef",
  "env": "prod",
  "service": "gateway"
}
```

### JSON Format (Datadog)

```json
{
  "timestamp": "2026-01-12T15:08:14.123Z",
  "level": "INFO",
  "module": "ACCESS",
  "method": "GET",
  "path": "/api/users",
  "status": 200,
  "latency_ms": 12.5,
  "client_ip": "10.0.0.1",
  "user_agent": "Mozilla/5.0...",
  "host": "api.example.com",
  "dd.trace_id": "1234567890123456789",
  "dd.span_id": "9876543210987654321",
  "env": "prod",
  "service": "gateway"
}
```

## Trace Propagation

Trace headers are automatically propagated to:

1. **Backend services**: Via the backend factory request executor wrapper
2. **IDP endpoints**: Token exchange and userinfo requests in the authenticator plugin

This enables end-to-end distributed tracing across the gateway and all downstream services.
