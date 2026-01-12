# HOG Local Development Stack

Docker-based development environment for HOG gateway with full observability (logs, traces, metrics).

## Architecture

```
                                    ┌─────────────────────────────────────────────────────────────────┐
                                    │                    Observability Stack                          │
┌──────────┐      ┌───────────────┐ │  ┌─────────────┐     ┌───────────┐     ┌──────────────┐        │
│ Browser  │─────▶│ nginx (:3000) │ │  │ alloy       │────▶│ tempo     │────▶│ grafana      │        │
└──────────┘      └───────────────┘ │  │ (:4317/4318)│     │ (:3200)   │     │ (:3001)      │        │
                         │          │  └──────┬──────┘     └───────────┘     └──────────────┘        │
                         ▼          │         │                  ▲                   ▲               │
                  ┌─────────────┐   │         │                  │                   │               │
                  │ hog (:8080) │───┼─────────┼──────────────────┴───────────────────┘               │
                  │     (:8090) │   │         │                                                       │
                  └──────┬──────┘   │         ▼                                                       │
                         │          │  ┌───────────┐     ┌────────────┐                               │
                  ┌──────▼──────┐   │  │ loki      │     │ prometheus │                               │
                  │ dex (:5556) │   │  │ (:3100)   │     │ (:9090)    │                               │
                  │    (IdP)    │   │  └───────────┘     └────────────┘                               │
                  └─────────────┘   └─────────────────────────────────────────────────────────────────┘
```

## Quick Start

```bash
cd tests/local-stack

# Build and start all services
podman compose up --build

# Open browser
open http://localhost:3000      # Test UI
open http://localhost:3001      # Grafana (admin/admin)
```

## Test Credentials

| Field | Value |
|-------|-------|
| Email | `test@example.com` |
| Password | `password` |

## Services

### Application Services

| Service | Port | Description |
|---------|------|-------------|
| nginx | 3000 | Reverse proxy + test UI |
| hog | 8080 (internal) | KrakenD gateway with plugins |
| hog | 8090 | Prometheus metrics endpoint |
| dex | 5556 | OIDC identity provider |

### Observability Stack

| Service | Port | Description |
|---------|------|-------------|
| grafana | 3001 | Visualization dashboards (admin/admin) |
| alloy | 4317, 4318 | OTLP gRPC/HTTP receivers |
| alloy | 12345 | Alloy internal UI |
| tempo | 3200 | Trace query API |
| loki | 3100 | Log query API |
| prometheus | 9090 | Metrics UI |

## Observability Features

### Trace-to-Log Correlation

1. Open Grafana at http://localhost:3001
2. Navigate to **Explore** → Select **Tempo**
3. Search for traces by service `hog-gateway`
4. Click a trace → Click **Logs for this span** to jump to correlated logs

### Pre-provisioned Dashboard

Navigate to **Dashboards** → **HOG Gateway** for:
- Request rate (req/s)
- Latency percentiles (p50, p95, p99)
- Error rate
- Live logs stream
- Recent traces

### Direct Log Queries

```logql
# All gateway logs
{job="docker", container=~".*hog.*"} | json

# Filter by trace ID
{job="docker"} | json | trace_id="<your-trace-id>"

# Errors only
{job="docker", container=~".*hog.*"} | json | level="ERROR"
```

## Test Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `/oauth/simple-auth` | No | Start login flow |
| `/oauth/userinfo` | Cookie | Get user info (plugin) |
| `/oauth/logout` | Cookie | Clear session |
| `/protected/userinfo` | JWT | Get user info (via KrakenD validator) |
| `/protected-static/*` | Cookie | Protected static content |
| `/static/*` | No | Public static content |

## Configuration Files

| File | Description |
|------|-------------|
| `hog/krakend.json` | Gateway configuration |
| `dex/dex-config.yaml` | IdP configuration |
| `nginx/nginx.conf` | Reverse proxy config |
| `alloy/config.alloy` | Telemetry collector config |
| `tempo/tempo.yaml` | Trace storage config |
| `loki/loki.yaml` | Log storage config |
| `prometheus/prometheus.yaml` | Metrics scrape config |
| `grafana/provisioning/` | Datasources & dashboards |

## Environment Variables

Set in `docker-compose.yaml`:

```yaml
environment:
  # IDP Configuration
  - IDP_ISSUER=http://dex:5556
  - IDP_CLIENT_ID=krakend
  - IDP_CLIENT_SECRET=krakend-secret
  - AUTH_COOKIE_KEY=abcdefghijklmnopqrstuvwxyz123456
  # Telemetry
  - TRACE_FORMAT=otel
  - OTEL_SERVICE_NAME=hog-gateway
  - OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318
```

## Logs

```bash
# All services
podman compose logs -f

# Just hog gateway
podman compose logs -f hog

# Observability stack
podman compose logs -f alloy tempo loki
```

### Log Collection for Grafana

Since Podman on macOS doesn't expose a Docker socket, logs must be collected using the helper script:

```bash
# Start log collector (runs in background, pushes logs to Loki)
./collect-logs.sh &

# Or run in foreground to see logs
./collect-logs.sh
```

The log collector:
- Tails the HOG container logs
- Parses JSON fields (level, trace_id, etc.)
- Pushes to Loki with proper labels for trace correlation

**Note:** Run the log collector after `podman compose up` for logs to appear in Grafana.

## Cleanup

```bash
# Stop services
podman compose down

# Stop and remove volumes (clears all data)
podman compose down -v
```
