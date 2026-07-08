# pkg/logging

Structured logging for the HOG gateway: JSON application logs and per-request
HTTP access logs, each carrying OTEL/Datadog **trace context** so any log line
can be pivoted back to its distributed trace.

It replaces KrakenD's default text logger and is wired in automatically when the
gateway boots. You don't call it from code — you turn it on through the
`telemetry/logging` block in `krakend.json`.

## Enable it

```json
{
  "extra_config": {
    "telemetry/logging": {
      "level": "INFO",
      "format": "json",
      "stdout": true,
      "access_log": true,
      "skip_paths": ["/__health"],
      "trace_format": "otel",
      "tags": { "env": "prod", "service": "gateway" }
    }
  }
}
```

| Option | Default | What it does |
|--------|---------|--------------|
| `level` | `INFO` | Minimum level emitted (`DEBUG`, `INFO`, `WARNING`, `ERROR`, `CRITICAL`). |
| `format` | text | `"json"` for structured logs; omit/empty for plain text. |
| `stdout` | `false` | Write to stdout — recommended in containers/k8s. |
| `access_log` | `true` | Emit one structured line per HTTP request (method, path, status, latency, client IP, trace ids). |
| `skip_paths` | `[]` | Paths excluded from access logging (e.g. `["/__health", "/__debug"]`). |
| `trace_format` | `otel` | Trace-id shape in logs and propagation: `otel` (W3C `trace_id`/`span_id`), `datadog` (`dd.trace_id`/`dd.span_id`), or `both`. |
| `tags` | `{}` | Extra `field → value` map stamped onto every log line. A value of `$VAR` or `${VAR}` is resolved from the environment at startup (unset → empty); literals pass through. e.g. `{"version": "$HOG_VERSION", "pod": "$HOSTNAME"}`. |

## Make traces line up

Set the authenticator plugin's `TRACE_FORMAT` env var to the same value as
`trace_format` so the IdP token-exchange and userinfo calls propagate matching
trace headers — otherwise the login hop shows up as a separate trace.

```bash
TRACE_FORMAT=otel      # or: datadog
```

## Full guide

[docs/observability.md](../../docs/observability.md) covers trace-to-log
correlation, ready-to-use OTEL and Datadog configurations, and the local
Grafana/Loki/Tempo stack for exploring it all.
