# HOG Intermediate Image

A pre-configured HOG (Highly Over-engineered Gateway) Docker image with flexible configuration, environment overlays, and reusable fragments for downstream applications.

## Overview

This intermediate image provides:

- ✅ **Flexible Configuration** - Go template-based config with JSON settings
- ✅ **Environment Overlays** - `local`, `nprod`, `prod` settings out of the box
- ✅ **Pre-loaded Plugins** - `hog-authenticator` and `hog-static-content`
- ✅ **Conditional OAuth** - Only validates secrets when `USE_OAUTH=1`
- ✅ **Reusable Fragments** - Partials for telemetry, CORS, auth, static content
- ✅ **Override Mechanism** - Add custom partials without modifying base config

## Quick Start

### Downstream Dockerfile

```dockerfile
FROM ghcr.io/paulopiriquito/hog:intermediate

# Add custom endpoints
COPY ./partials/custom/ /etc/krakend/partials/custom/

# Set environment
ENV HOG_ENV=prod
ENV STATIC_HOST=http://webapp:3000
```

### Docker Compose

```yaml
services:
  gateway:
    image: my-org/my-gateway:latest
    build:
      context: .
      dockerfile: Dockerfile
    environment:
      - HOG_ENV=prod
      - STATIC_HOST=http://webapp:3000
      - CORS_ALLOW_ORIGINS=https://example.com,https://www.example.com
      # Telemetry tags (optional overrides)
      - ENV=production
      - SERVICE_VERSION=v1.2.3
      # OAuth (optional)
      - USE_OAUTH=1
      - IDP_ISSUER=https://idp.example.com
      - IDP_CLIENT_ID=my-app
      - IDP_CLIENT_SECRET=${IDP_CLIENT_SECRET}
      - AUTH_COOKIE_KEY=${AUTH_COOKIE_KEY}
    ports:
      - "8080:8080"
      - "8090:8090"  # Prometheus metrics
```

## Environment Variables

### Core Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `HOG_ENV` | `local` | Environment overlay: `local`, `nprod`, `prod` |
| `PORT` | `8080` | Gateway listen port |
| `TIMEOUT` | `30s` | Default request timeout |

### Static Content

| Variable | Default | Description |
|----------|---------|-------------|
| `STATIC_HOST` | `http://localhost:3000` | Upstream static content server |
| `STATIC_PATH_PREFIX` | `/*` | Path prefix for static routes |
| `STATIC_AUTH_REQUIRED` | `false` | Require auth for static content |

### OAuth / OIDC (when `USE_OAUTH=1`)

| Variable | Default | Description |
|----------|---------|-------------|
| `USE_OAUTH` | `0` | Enable OAuth authentication |
| `IDP_ISSUER` | - | **Required** OIDC provider URL |
| `IDP_CLIENT_ID` | `hog-gateway` | OAuth client ID |
| `IDP_CLIENT_SECRET` | - | **Required** OAuth client secret |
| `AUTH_COOKIE_KEY` | - | **Required** 32-byte session encryption key |
| `AUTH_COOKIE_NAME` | `hog_session` | Session cookie name |

### CORS

| Variable | Default | Description |
|----------|---------|-------------|
| `CORS_ALLOW_ORIGINS` | (per env) | Comma-separated allowed origins |

### Telemetry

| Variable | Default | Description |
|----------|---------|-------------|
| `ENV` | (per env) | Override `log_tags.env` for runtime environment tagging |
| `SERVICE_NAME` | (per env) | Override `log_tags.service` for service identification |
| `SERVICE_VERSION` | (per env) | Override `log_tags.version` for version tagging |
| `LOG_LEVEL` | `DEBUG`/`INFO`/`WARN` | Log level (per environment) |
| `LOG_FORMAT` | `json` | Log format: `json`, `logstash`, `custom` |
| `TRACE_FORMAT` | `otel` | Trace format: `otel`, `datadog`, `both` |
| `OTEL_ENABLED` | `true` | Enable OpenTelemetry |
| `OTEL_SERVICE_NAME` | (per env) | OTEL service name |
| `OTEL_EXPORTER_HOST` | `localhost`/`otel-collector` | OTEL collector host |
| `OTEL_EXPORTER_PORT` | `4317` | OTEL collector port |
| `METRICS_LISTEN_ADDRESS` | `:8090` | Prometheus metrics endpoint |

### Backend

| Variable | Default | Description |
|----------|---------|-------------|
| `HEALTH_BACKEND_HOST` | `http://localhost:8080` | Backend for health checks |
| `HEALTH_BACKEND_PATH` | `/__health` | Backend health path |

## Directory Structure

```
/etc/krakend/
├── config/
│   └── krakend.tmpl          # Root configuration template
├── settings/
│   ├── local.json            # Local development settings
│   ├── nprod.json            # Non-production settings
│   └── prod.json             # Production settings
├── partials/
│   ├── plugin-loader.tmpl    # Plugin loader configuration
│   ├── telemetry-config.tmpl # Logging, OTEL, metrics
│   ├── cors-config.tmpl      # CORS security configuration
│   ├── authenticator.tmpl    # OAuth/OIDC authentication plugin
│   ├── static-content.tmpl   # Static content serving plugin
├── templates/                # Go template helpers (reserved for future use)
│   └── README.md
├── plugins/                  # Compiled plugins (.so) - mounted at runtime
└── entrypoint.sh             # Startup script
```

**Note:** KrakenD Community Edition requires all `.tmpl` files to be in the root `/etc/krakend/partials/` directory. Nested directories for partials are only supported in the Enterprise Edition. The subdirectories (`plugins/`, `telemetry/`, `security/`) are used for documentation and organization purposes only.

## Customization

### Adding Custom Endpoints

Create `partials/custom/endpoints.tmpl`:

```json
{
  "endpoint": "/api/v1/users",
  "method": "GET",
  "backend": [
    {
      "host": ["http://users-service:8080"],
      "url_pattern": "/users"
    }
  ]
}
```

### Overriding Static Routes

Create `partials/custom/static-routes.tmpl`:

```json
{
  "path-prefix": "/app/*",
  "service-host": "http://frontend:3000",
  "auth": true
},
{
  "path-prefix": "/public/*",
  "service-host": "http://frontend:3000",
  "auth": false
}
```

### Adding Custom Settings

You can extend settings by adding environment variables that follow the naming convention, or by mounting a custom settings file.

### Customizing Telemetry Tags

Telemetry tags support a hybrid approach for maximum flexibility:

**Environment Variables Override** (Runtime)

Three special tags can be overridden via environment variables:
- `ENV` → Overrides `log_tags.env`
- `SERVICE_NAME` → Overrides `log_tags.service`
- `SERVICE_VERSION` → Overrides `log_tags.version`

**Configuration File Tags** (Base + Additional)

Define base values and additional custom tags in your settings file (e.g., `prod.json`):

```json
"log_tags": {
  "env": "prod",
  "service": "hog-gateway",
  "version": "v1.0.0",
  "region": "us-east-1",
  "team": "platform",
  "cluster": "k8s-prod-01"
}
```

**Result**

When you run with environment variables:
```bash
ENV=canary SERVICE_VERSION=v1.1.0-rc1 docker run ...
```

The final tags will be:
```json
{
  "env": "canary",              // ← from ENV
  "service": "hog-gateway",     // ← from config (no override)
  "version": "v1.1.0-rc1",      // ← from SERVICE_VERSION
  "region": "us-east-1",        // ← from config
  "team": "platform",           // ← from config
  "cluster": "k8s-prod-01"      // ← from config
}
```

This allows you to:
- Override critical tags at deployment time (canary, blue/green, version bumps)
- Keep additional organizational tags in configuration
- Have sensible defaults when environment variables aren't set

## Default Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/__health` | GET | Proxied health check |
| `/*` | * | Static content (configurable) |

When `USE_OAUTH=1`:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/oauth/simple-auth` | GET | Initiate OAuth flow |
| `/oauth/callback` | GET | OAuth callback |
| `/oauth/logout` | GET/POST | Logout |
| `/oauth/userinfo` | GET | Get user info |
| `/oauth/token` | POST | Token endpoint |

## Building

```bash
cd dist/
docker build -t ghcr.io/paulopiriquito/hog:intermediate .
```

## Example: Production Deployment

```dockerfile
# Dockerfile
FROM ghcr.io/paulopiriquito/hog:intermediate

# Add API endpoints
COPY partials/custom/ /etc/krakend/partials/custom/

ENV HOG_ENV=prod
```

```yaml
# docker-compose.yaml
services:
  gateway:
    build: .
    environment:
      - STATIC_HOST=http://webapp:3000
      - CORS_ALLOW_ORIGINS=https://myapp.com
      # Telemetry tags
      - ENV=production
      - SERVICE_NAME=api-gateway
      - SERVICE_VERSION=v2.1.0
      # OAuth
      - USE_OAUTH=1
      - IDP_ISSUER=https://auth.myapp.com
      - IDP_CLIENT_ID=gateway
      - IDP_CLIENT_SECRET=${IDP_SECRET}
      - AUTH_COOKIE_KEY=${COOKIE_KEY}
      # Observability
      - OTEL_EXPORTER_HOST=otel-collector
    ports:
      - "8080:8080"
```

## Troubleshooting

### Configuration Validation Failed

The entrypoint runs `krakend check` before starting. Check logs for template errors:

```bash
docker run --rm -e HOG_ENV=local ghcr.io/paulopiriquito/hog:intermediate
```

### OAuth Secrets Missing

When `USE_OAUTH=1`, these are required:
- `AUTH_COOKIE_KEY` (32+ characters)
- `IDP_CLIENT_SECRET`

### View Compiled Configuration

```bash
docker run --rm -e HOG_ENV=local \
  ghcr.io/paulopiriquito/hog:intermediate \
  cat /etc/krakend/compiled/krakend.json
```
