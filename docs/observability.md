# Structured Observability with OTEL/Datadog Trace Correlation

This document describes how to configure structured logging with trace correlation for the HOG gateway.

## Configuration Options

### `telemetry/logging` Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `format` | string | `""` | Log format: `"json"`, `"logstash"`, `"custom"`, or empty for text |
| `access_log` | bool | `true` | Enable/disable access logging for HTTP requests |
| `skip_paths` | []string | `[]` | Paths to exclude from access logging (e.g., `["/__health"]`) |
| `trace_format` | string | `"otel"` | Trace ID format: `"otel"`, `"datadog"`, or `"both"` |
| `tags` | object | `{}` | Custom tags to include in all log entries |

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

### Production (Datadog Agent)

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
        "env": "prod",
        "service": "gateway",
        "version": "v1.0.0"
      }
    },
    "telemetry/opentelemetry": {
      "service_name": "hog-gateway",
      "service_version": "v1.0.0",
      "skip_paths": ["/__health"],
      "exporters": {
        "otlp": [{
          "name": "datadog-agent",
          "host": "datadog-agent",
          "port": 4317,
          "use_http": false
        }]
      }
    }
  }
}
```

## Environment Variables

For the authenticator plugin, set `TRACE_FORMAT` to match your logging configuration:

```bash
# Local development
TRACE_FORMAT=otel

# Production with Datadog
TRACE_FORMAT=datadog
```

## Docker Compose Setup (Local Development)

The `tests/local-stack/docker-compose.yaml` includes:

- **alloy**: Grafana Alloy - unified telemetry collector for logs, traces, and metrics
- **tempo**: Grafana Tempo for distributed tracing (API at http://localhost:3200)
- **loki**: Grafana Loki for log aggregation (API at http://localhost:3100)
- **prometheus**: Prometheus for metrics (UI at http://localhost:9090)
- **grafana**: Grafana for visualization (UI at http://localhost:3001, admin/admin)

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
