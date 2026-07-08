# hog-static-content

A KrakenD HTTP server plugin that proxies requests to static file servers with optional authentication.

## Architecture

```
┌─────────┐         ┌──────────────────┐         ┌────────────────┐
│ Browser │◀───────▶│     KrakenD      │◀───────▶│ Static Server  │
└─────────┘         │ + static-content │         │ (SPA, CDN)     │
                    └──────────────────┘         └────────────────┘
                             │
                    ┌────────▼─────────┐
                    │  KrakenD APIs    │
                    │  (endpoints:[])  │
                    └──────────────────┘
```

**Routing Logic:**
1. Request matches `service-gateway.path-prefix` → routed to KrakenD endpoints
2. Request matches `static[].path-prefix` → proxied to static server
3. If `auth: true` → validates session cookie before proxying

## Features

- **Wildcard Routing** - Flexible path patterns (`/app/*`, `/assets/*`)
- **Multiple Upstreams** - Different static servers per path
- **Optional Auth** - Per-route authentication via `hog-authenticator`
- **Auto-Redirect** - Unauthenticated users redirected to login, then back to original path
- **Header Control** - Strips hop-by-hop headers before proxying; opt out with `keep-unsafe-headers`

## Configuration

### Basic (No Auth)

```json
{
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-static-content"],
      "hog-static-content": {
        "static": [
          {
            "path-prefix": "/app/*",
            "service-host": "http://frontend:3000"
          }
        ],
        "service-gateway": {
          "path-prefix": ["/api/*"]
        }
      }
    }
  }
}
```

### With Authentication

Requires `hog-authenticator` plugin to be loaded.

```json
{
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-authenticator", "hog-static-content"],
      "hog-static-content": {
        "static": [
          {
            "path-prefix": "/public/*",
            "service-host": "http://cdn:80",
            "auth": false
          },
          {
            "path-prefix": "/dashboard/*",
            "service-host": "http://spa:3000",
            "auth": true
          }
        ],
        "auth": {
          "simple-auth-url": "/oauth/simple-auth"
        },
        "service-gateway": {
          "path-prefix": ["/api/*", "/oauth/*"]
        }
      },
      "hog-authenticator": {
        "idp": { "type": "oidc" }
      }
    }
  }
}
```

### Configuration Reference

#### `static[]` - Static server routes

| Field | Type | Description |
|-------|------|-------------|
| `path-prefix` | string | URL pattern (supports `*` wildcard) |
| `service-host` | string | Upstream server URL |
| `auth` | bool | Require authentication (default: `false`) |
| `keep-unsafe-headers` | bool | Forward all headers including sensitive ones (default: `false`) |

#### `service-gateway` - KrakenD endpoint routes

| Field | Type | Description |
|-------|------|-------------|
| `path-prefix` | []string | Patterns routed to KrakenD endpoints |

#### `auth` - Authentication settings

| Field | Type | Description |
|-------|------|-------------|
| `simple-auth-url` | string | Login redirect URL (default: `/oauth/simple-auth`) |
| `session-cookie-name` | string | Cookie name (default: `auth_session`) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AUTH_COOKIE_KEY` | 32-byte session decryption key |
| `AUTH_COOKIE_NAME` | Cookie name override |

## Auth Flow for Protected Routes

When `auth: true` and user is not authenticated:

```
1. GET /dashboard/home
2. No valid session cookie → 302 Redirect to /oauth/simple-auth?redirect=/dashboard/home
3. User logs in via IdP
4. After login → 302 Redirect back to /dashboard/home
5. Request proxied to static server
```

## Routing Priority

1. **OAuth routes** (`/oauth/*`) - Always handled by authenticator (hardcoded)
2. **Gateway routes** (`service-gateway.path-prefix`) - Routed to KrakenD endpoints
3. **Static routes** (`static[].path-prefix`) - Proxied to static servers
4. **Fallback** - Passed to KrakenD default handler

## Example: SPA with Protected Dashboard

```json
{
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-authenticator", "hog-static-content"],
      "hog-static-content": {
        "static": [
          {
            "path-prefix": "/*",
            "service-host": "http://spa:3000",
            "auth": false
          },
          {
            "path-prefix": "/admin/*",
            "service-host": "http://spa:3000",
            "auth": true
          }
        ],
        "auth": {
          "simple-auth-url": "/oauth/simple-auth"
        },
        "service-gateway": {
          "path-prefix": ["/api/*"]
        }
      },
      "hog-authenticator": {
        "idp": { "type": "oidc" }
      }
    }
  },
  "endpoints": [
    {
      "endpoint": "/api/users",
      "backend": [{ "host": ["http://users-service:8080"], "url_pattern": "/users" }],
      "extra_config": {
        "auth/validator": {
          "alg": "RS256",
          "jwk_url": "http://idp:5556/keys",
          "disable_jwk_security": true
        }
      }
    }
  ]
}
```

> ⚠️ **Important:** Routes are matched in order. Place more specific routes before wildcards.

## Security Notes

- **Only hop-by-hop headers are stripped** before proxying (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `Te`, `Trailer`, `Transfer-Encoding`, `Upgrade`). The request's `Authorization` (the `Bearer <jwt>` injected by `hog-authenticator`) and `Cookie` (the session cookie) **are forwarded to the static upstream**, and `X-Forwarded-For` / `X-Forwarded-Host` / `X-Forwarded-Proto` are added. Point `auth: true` routes only at upstreams you trust.
- `keep-unsafe-headers: true` forwards every header verbatim, including the hop-by-hop ones above. Use only for trusted internal services.
- The upstream receives the **full request path including the matched prefix** (the prefix is not stripped) — e.g. `/app/index.html` is proxied as `/app/index.html`. The upstream must serve under that prefix, or rewrite it.
- Use `service-gateway` (or order specific routes before a `/*` catch-all) to keep API routes from being served as static content.
