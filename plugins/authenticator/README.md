# hog-authenticator

A KrakenD HTTP server plugin that provides OAuth 2.0 / OIDC authentication for Single Page Applications (SPAs).

## Architecture

```
┌─────────┐         ┌──────────────────┐         ┌─────┐
│   SPA   │◀───────▶│     KrakenD      │◀───────▶│ IdP │
└─────────┘         │ + authenticator  │         └─────┘
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │ Backend Services │
                    └──────────────────┘
```

**Flow:**
1. User accesses protected resource → redirected to IdP login
2. After login, plugin receives authorization code
3. Plugin exchanges code for JWT, stores in encrypted HttpOnly cookie
4. Subsequent requests: plugin decrypts cookie → injects `Authorization: Bearer <jwt>` header
5. KrakenD's `auth/validator` validates the JWT

## Features

- **OIDC Discovery** - Automatic endpoint configuration via `.well-known/openid-configuration`
- **PKCE Support** - Proof Key for Code Exchange for enhanced security
- **Encrypted Cookies** - AES-256-GCM encrypted HttpOnly cookies (XSS protection)
- **Stateless Sessions** - Horizontal scaling support with signed state tokens
- **User Headers** - Injects `X-User-Id`, `X-User-Email`, `X-User-Name` for backends

## Configuration

### Environment Variables (Recommended)

| Variable | Description | Required |
|----------|-------------|----------|
| `IDP_ISSUER` | OIDC provider URL (e.g., `http://dex:5556`) | Yes |
| `IDP_CLIENT_ID` | OAuth client ID | Yes |
| `IDP_CLIENT_SECRET` | OAuth client secret | Yes |
| `AUTH_COOKIE_KEY` | 32-byte session encryption key | Yes* |
| `AUTH_COOKIE_NAME` | Cookie name (default: `auth_session`) | No |

*If not provided, a random key is generated (not suitable for multi-instance deployments).

### Minimal Configuration

When using environment variables, only specify the IdP type:

```json
{
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-authenticator"],
      "hog-authenticator": {
        "idp": {
          "type": "oidc"
        }
      }
    }
  }
}
```

### Full Configuration

```json
{
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-authenticator"],
      "hog-authenticator": {
        "idp": {
          "type": "oidc",
          "issuer": "http://dex:5556",
          "well-known": "/.well-known/openid-configuration",
          "client-id": "my-client",
          "client-secret": "my-secret"
        },
        "config": {
          "simple-auth-url": "/oauth/simple-auth",
          "token-url": "/oauth/token",
          "callback-url": "/oauth/callback",
          "logout-url": "/oauth/logout",
          "user-info-url": "/oauth/userinfo",
          "scopes": "openid profile email",
          "session-cookie-name": "auth_session"
        }
      }
    }
  }
}
```

### Defaults

All `config` fields have sensible defaults:

| Field | Default |
|-------|---------|
| `simple-auth-url` | `/oauth/simple-auth` |
| `token-url` | `/oauth/token` |
| `callback-url` | `/oauth/callback` |
| `logout-url` | `/oauth/logout` |
| `user-info-url` | `/oauth/userinfo` |
| `scopes` | `openid profile email` |
| `session-cookie-name` | `auth_session` |
| `well-known` | `/.well-known/openid-configuration` |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/oauth/simple-auth` | GET | Initiates login flow (redirects to IdP) |
| `/oauth/callback` | GET | OAuth callback (handled automatically) |
| `/oauth/token` | POST | PKCE token exchange for SPAs |
| `/oauth/userinfo` | GET | Returns user info from IdP |
| `/oauth/logout` | POST | Clears session cookie |

## Protected Endpoints with auth/validator

For endpoints requiring JWT validation, use KrakenD's built-in `auth/validator`:

```json
{
  "endpoint": "/api/protected",
  "extra_config": {
    "auth/validator": {
      "alg": "RS256",
      "audience": ["my-client"],
      "jwk_url": "http://dex:5556/keys",
      "disable_jwk_security": true
    }
  }
}
```

> ⚠️ **Warning:** Set `disable_jwk_security: true` only for HTTP JWK endpoints (development). Use HTTPS in production.

## Usage Example (SPA)

```javascript
// Check if authenticated
async function checkAuth() {
  const res = await fetch('/oauth/userinfo', { credentials: 'include' });
  return res.ok;
}

// Login
function login() {
  window.location.href = '/oauth/simple-auth';
}

// Logout
async function logout() {
  await fetch('/oauth/logout', { method: 'POST', credentials: 'include' });
}

// Access protected API (cookie sent automatically)
async function fetchData() {
  const res = await fetch('/api/protected', { credentials: 'include' });
  return res.json();
}
```

## Security Considerations

- **JWT never exposed to browser** - Stored in encrypted HttpOnly cookie
- **PKCE enabled** - Protects against authorization code interception
- **Signed state tokens** - Prevents CSRF attacks
- **Session expiry** - Cookie expires when JWT expires

## Testing

```bash
# Unit tests
cd plugins/authenticator
go test -v .

# Integration tests
cd tests/local-stack
podman compose up --build
# Open http://localhost:3000 (test UI)
# Open http://localhost:3001 (Grafana observability)
```
