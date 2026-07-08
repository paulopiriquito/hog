# HOG Local Development Stack

Docker-based development environment for HOG gateway with OTEL-native
observability (traces and metrics in OpenObserve; structured JSON logs on stdout).

## Architecture

```
┌──────────┐      ┌───────────────┐      ┌─────────────┐
│ Browser  │─────▶│ nginx (:3000) │─────▶│ hog (:8080) │
└──────────┘      └───────────────┘      └──────┬──────┘
                  ┌─────────────┐                │ OTLP gRPC (traces, metrics)
                  │ dex (:5556) │◀── hog          ▼
                  │    (IdP)    │        ┌──────────────────────┐
                  └─────────────┘        │ otel-collector       │
                                         │ (:4317 gRPC/:4318)   │
                                         └──────────┬───────────┘
                                                    │ OTLP/HTTP + Basic auth
                                                    ▼
                                         ┌──────────────────────┐
                                         │ openobserve          │
                                         │ (:5080 UI + API)     │
                                         └──────────────────────┘
```

A thin OpenTelemetry Collector sits in front of OpenObserve because OpenObserve's
ingest needs a Basic-auth header that KrakenD's own OTLP exporter can't add.

## Quick Start

```bash
cd tests/local-stack

# Build and start all services
podman compose up --build

# Open browser
open http://localhost:3000      # Test UI
open http://localhost:5080      # OpenObserve (root@example.com / Complexpass123)
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
| openobserve | 5080 | OTEL-native UI + API for traces & metrics (root@example.com / Complexpass123) |
| otel-collector | 4317, 4318 | OTLP gRPC/HTTP receivers; forwards to OpenObserve |

## Observability Features

### Viewing traces & metrics

1. Open OpenObserve at http://localhost:5080 and log in (`root@example.com` / `Complexpass123`).
2. **Traces** → stream `default` → search/filter by service `hog-gateway`. Click a
   trace to see its span tree (e.g. `/e2e-api/headers` → proxy → backend).
3. **Metrics** → streams like `krakend_proxy_duration_*`, `krakend_backend_duration_*`,
   `http_server_duration_*`.

Traces and metrics flow `hog → otel-collector → openobserve` over OTLP. Local
flush is tuned for fast visibility (`ZO_MAX_FILE_RETENTION_TIME=5`), so data
appears within a few seconds.

### Trace-to-log correlation

hog emits structured JSON access logs on **stdout** with the same `trace_id`
OpenObserve stores, so you can pivot between them by hand:

```bash
# grab a request's trace_id from the gateway logs
podman compose logs hog | grep '"module":"ACCESS"'
# {"module":"ACCESS","path":"/e2e-api/headers","trace_id":"257fa7e4...","span_id":"..."}
```

Then search that `trace_id` in OpenObserve's Traces view. (Shipping the stdout
logs into OpenObserve is a planned follow-up; for now logs stay on stdout.)

## Test Endpoints

The OAuth and validator endpoints run on the core stack. The static-content and
echo-API demos (`/static/*`, `/e2e-static/*`, `/e2e-api/*`) proxy to the
`e2e-web` / `e2e-api` containers, which live in a **separate compose file** —
start it alongside the core stack to exercise those routes (see
[Static-content & API demos](#static-content--api-demos)).

| Endpoint | Auth | Demonstrates | Needs E2E stack |
|----------|------|--------------|-----------------|
| `/oauth/simple-auth` | No | Server-driven login (redirects to IdP, then back). Accepts `?redirect=<path>` | — |
| `/oauth/pkce-init` | No | Client-driven PKCE: returns the IdP `authorization_url` for a SPA to drive itself | — |
| `/oauth/userinfo` | Cookie | Live userinfo + `mapped` roles from `forward.headers` | — |
| `/oauth/logout` | Cookie | Clears the session cookie | — |
| `/protected/userinfo` | JWT | Userinfo behind KrakenD's `auth/validator` | — |
| `/static/*` | No | Public static content (no cookie required) | yes (`e2e-web`) |
| `/protected-static/*` | Cookie | Protected static content (echoes via httpbin.org — needs internet) | — |
| `/e2e-static/*` | Cookie | Protected static content, e.g. `/e2e-static/protected.html` | yes (`e2e-web`) |
| `/e2e-api/headers` | JWT | Echoes the `X-User-*` / `Authorization` headers hog injects | yes (`e2e-api`) |

### Static-content & API demos

The routes marked **Needs E2E stack** proxy to services defined in
`tests/e2e/docker-compose.e2e.yaml`. Bring them up on the same network after the
core stack is running:

```bash
# core stack first (creates the shared network)
podman compose -f tests/local-stack/docker-compose.yaml up -d --build

# then the e2e web/api upstreams
podman compose -f tests/e2e/docker-compose.e2e.yaml up -d --build

# try a protected static page — bounces to Dex login, then serves the page
open http://localhost:3000/e2e-static/protected.html
```

## Configuration Files

| File | Description |
|------|-------------|
| `hog/krakend.json` | Gateway configuration |
| `dex/dex-config.yaml` | IdP configuration |
| `nginx/nginx.conf` | Reverse proxy config |
| `otel-collector/config.yaml` | OTLP receivers → OpenObserve exporter |

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
  - OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
```

## Logs

hog writes structured JSON logs (with `trace_id`) to stdout — read them with:

```bash
# Just the gateway
podman compose logs -f hog

# Only access logs
podman compose logs hog | grep '"module":"ACCESS"'

# All services
podman compose logs -f
```

Traces and metrics live in OpenObserve (http://localhost:5080); see
[Observability Features](#observability-features).

## Cleanup

```bash
# Stop services
podman compose down

# Stop and remove volumes (clears all data)
podman compose down -v
```
