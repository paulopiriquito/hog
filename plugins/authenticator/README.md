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

## Forwarding userinfo properties as headers

The plugin can be configured to project any property from the IdP's `/userinfo` response into a custom HTTP header forwarded to upstream backends. This **replaces** the default `X-User-Id` / `X-User-Email` / `X-User-Name` family with an explicit allowlist.

> **Migration note:** As of this plugin version, the legacy hardcoded `X-User-Id` / `X-User-Email` / `X-User-Name` headers are emitted only when `forward.headers` is **absent**. To customize identity headers or forward additional properties (such as roles), configure `forward.headers` and declare every header you want backends to see.

### Configuration

```json
"hog-authenticator": {
  "idp":    { "type": "oidc" },
  "config": { "scopes": "openid profile email groups" },
  "forward": {
    "headers": [
      { "claim": "sub",                    "header": "X-User-Id" },
      { "claim": "email",                  "header": "X-User-Email" },
      { "claim": "name",                   "header": "X-User-Name" },
      { "claim": "employeeNumber",         "header": "X-User-EmployeeNumber" },
      {
        "claim":  "memberof",
        "header": "X-User-Roles",
        "mapping": [
          { "from": "cn=PT-LM-ROLE-KRONOS-USER,", "to": "KRONOS-USER" },
          { "from": "cn=GLOBAL-ROLE-GITHUB-",     "to": "GITHUB-MEMBER" }
        ]
      }
    ]
  }
}
```

### Behavior

| Claim type                  | `mapping` present | Result                                                                                                       |
|-----------------------------|-------------------|--------------------------------------------------------------------------------------------------------------|
| Scalar (string/number/bool) | no                | Header value is the stringified claim.                                                                       |
| Scalar                      | yes               | First rule whose `from` is a substring of the value wins; emit `to`. No match → header is not emitted.       |
| Array of scalars            | no                | Comma-joined values.                                                                                          |
| Array of scalars            | yes               | Filter (first-match-wins per value), rename to `to`, deduplicate, comma-join. Empty result → not emitted.    |
| Missing claim               | —                 | Header is not emitted; logged at debug.                                                                       |
| Wrong type (object, null)   | —                 | Header is not emitted; logged at warning.                                                                     |

### Data source

Mapped values come from the IdP's `/userinfo` response, fetched once at login and re-fetched whenever the SPA calls `/oauth/userinfo`. The result is stored in the encrypted session cookie, so per-request backend forwarding does not call the IdP.

### Refreshing mapped roles without a re-login

Have the SPA call `GET /oauth/userinfo`. The plugin:

1. Re-fetches userinfo from the IdP using the access token.
2. Re-applies `forward.headers`.
3. Updates the session cookie's Headers via `Set-Cookie`.
4. Returns the raw IdP JSON plus a top-level `mapped` object the SPA can read.

The next backend request carries the refreshed headers automatically.

### Allowlist vs. default behavior

- **`forward.headers` absent:** the plugin emits the hardcoded legacy headers `X-User-Id`, `X-User-Email`, `X-User-Name` from id_token claims (pre-feature behavior).
- **`forward.headers` present:** the plugin emits **only** the headers listed. If you still want `X-User-Id`, declare it in `forward.headers`. `Authorization: Bearer <jwt>` and `Identity: <id_token>` are emitted in both modes.

### Trust boundary

This feature emits identity- and role-information as HTTP headers. **Backends are expected to trust those headers only when they are reachable solely via the gateway** (e.g., a Kubernetes namespace with ingress routing only to the gateway). If a backend ever becomes reachable through any other path, header signing (HMAC or a gateway-issued short-lived JWT) must be added before exposing it. The plugin does not sign forwarded headers.

### Common IdP claim names

| IdP                                | Roles/groups claim    | Notes                                                          |
|------------------------------------|-----------------------|----------------------------------------------------------------|
| Dex (with `groups` scope)          | `groups`              | Static passwords' `groups` field; values are arbitrary strings.|
| Keycloak                           | `realm_access.roles`  | Use dotted path.                                                |
| LDAP-OIDC bridge                   | `memberof`            | Values are full LDAP DNs; use substring rules to filter/rename.|
| Okta                               | `groups`              | Requires `groups` scope and OIDC group claim configuration.    |
| Auth0                              | Custom-namespaced     | E.g., `https://myapp.com/roles`; depends on Rule/Action setup. |

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
