# Quick start: a Vue SPA and backend behind auth

Build a Vue 3 + Vite single-page app, serve it through HOG, reverse-proxy
`/api` to a backend, and put an OIDC login in front of both — the SPA and
the API it calls. HOG runs the whole login flow; the Vue app never sees a
client ID, a token, or a redirect.

!!! note "Prerequisites"
    - An OIDC client registered with a provider that supports discovery
      (Keycloak, Auth0, Okta, Dex, …): an issuer URL, a client ID, a client
      secret, and `http://localhost:8080/auth/callback` registered as a
      redirect URL.
    - Node.js (for the Vite build) and Docker.
    - The `hog-runtime` image built locally, as in
      [Serve a static site](quickstart-static.md)
      (`docker build -f build/Dockerfile.runtime -t hog-runtime .`).

## Folder structure

```text
my-app/
├── frontend/            # Vue 3 + Vite app
│   ├── package.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/…
├── gateway.yaml         # HOG: session + IdP + SPA route + reverse-proxy /api
├── Dockerfile           # multi-stage: node builds Vite → HOG serves
└── compose.yaml         # HOG + a demo backend
```

## 1. Scaffold the Vue app

```sh
mkdir my-app && cd my-app
npm create vite@latest frontend -- --template vue-ts
cd frontend && npm install
```

`vite.config.ts` builds to `dist/` with everything referenced from the
site root — leave `base` at `/`, which is also where HOG's `static` route
serves the SPA from:

```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: '/',
  plugins: [vue()],
})
```

Replace `frontend/src/App.vue` with something that calls the protected API
so you have something to observe once you're logged in:

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'

const me = ref<unknown>(null)

onMounted(async () => {
  const res = await fetch('/api/whoami')
  me.value = await res.json()
})
</script>

<template>
  <h1>Signed in</h1>
  <pre>{{ me }}</pre>
</template>
```

`fetch('/api/whoami')` is a same-origin call — the browser sends the
`hog_session` cookie automatically, no auth code needed in the SPA.

## 2. Write `gateway.yaml`

```yaml
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
  session:
    key: ${SESSION_KEY}
    ttl: 8h
---
kind: IdP
metadata: { name: default }
spec:
  type: oidc
  issuer: ${OIDC_ISSUER}
  clientID: ${OIDC_CLIENT_ID}
  clientSecret: ${OIDC_CLIENT_SECRET}
  redirectURL: http://localhost:8080/auth/callback
---
kind: Route
metadata: { name: app }
spec:
  match: /
  handler:
    type: static
    dir: /srv/web
  access: { auth: required }
---
kind: Route
metadata: { name: api }
spec:
  match: /api/
  handler:
    type: reverse-proxy
    upstream: http://backend:9000
    stripPrefix: /api
    forwardAccessToken: true
  access: { auth: required }
```

- The `app` route (`type: static`, inferred `type: app`) redirects an
  unauthenticated visit to `/auth/login` instead of returning `401` — the
  browser is sent straight into the login flow with no JS needed to detect
  "not logged in."
- The `api` route (`type: reverse-proxy`, inferred `type: service`) returns
  `401` instead when unauthenticated; `auth: required` is the default for
  service routes here, spelled out for clarity.
- `forwardAccessToken: true` puts the caller's access token on the request
  to the backend; it's off by default.
- `/auth/login`, `/auth/logout`, and `/auth/session` are mounted
  automatically because both `session` and an `IdP` are configured. Log out
  with a same-origin `POST` (`fetch('/auth/logout', { method: 'POST' })`) —
  it is `POST`-only, so a plain link (`GET`) returns `405` — no
  `Route` resources needed for them, and `redirectURL`'s path
  (`/auth/callback`) becomes the callback route the same way.

## 3. Write the Dockerfile

A multi-stage build: Node builds the Vite bundle, then it's copied onto
`hog-runtime` next to the config:

```dockerfile
# syntax=docker/dockerfile:1
FROM node:20-alpine AS frontend-build
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

FROM hog-runtime
COPY --from=frontend-build --chown=hog:hog /app/dist /srv/web
COPY --chown=hog:hog gateway.yaml /etc/hog/gateway.yaml
```

!!! warning
    Never `COPY` a secret (`OIDC_CLIENT_SECRET`, `SESSION_KEY`) into the
    image. `gateway.yaml` only ever holds `${VAR}` references; the values
    are injected as environment variables at container start, below.

## 4. Write `compose.yaml`

For this walkthrough, the backend is a two-line Python HTTP server that
echoes back whatever it received — enough to see exactly what HOG forwards
without writing a second service:

```yaml
services:
  gateway:
    build: .
    ports:
      - "8080:8080"
    read_only: true
    tmpfs:
      - /tmp
    environment:
      SESSION_KEY: ${SESSION_KEY}
      OIDC_ISSUER: ${OIDC_ISSUER}
      OIDC_CLIENT_ID: ${OIDC_CLIENT_ID}
      OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET}
    depends_on:
      - backend

  backend:
    image: python:3.12-alpine
    command:
      - python3
      - -c
      - |
        import json
        from http.server import BaseHTTPRequestHandler, HTTPServer
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                body = json.dumps({"path": self.path, "headers": dict(self.headers)}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(body)
        HTTPServer(("0.0.0.0", 9000), Handler).serve_forever()
```

## 5. Run it

The session cookie is sealed with AES-256-GCM, which needs a key of exactly
32 bytes; `openssl rand -hex 16` prints 32 hex characters:

```sh
export SESSION_KEY=$(openssl rand -hex 16)
export OIDC_ISSUER=https://idp.example.com
export OIDC_CLIENT_ID=hog-example
export OIDC_CLIENT_SECRET=your-client-secret

docker compose up --build
```

!!! success "Expected result"
    `"hog listening" addr=:8080` on stdout, and a `backend-1` container
    ready to echo requests.

## 6. Log in and observe

1. Open `http://localhost:8080/` in a browser. There's no session cookie
   yet and `/` requires auth, so HOG responds `302` to
   `/auth/login?return_to=%2F`, which redirects on to your IdP.
2. Authenticate at the IdP. It redirects back to
   `http://localhost:8080/auth/callback?code=...&state=...`; HOG verifies
   `state`, exchanges the code for tokens, verifies the ID token, and seals
   the result into the `hog_session` cookie (`SameSite=Lax`, `HttpOnly`,
   `Secure` once you're behind TLS).
3. HOG redirects back to `/`. The Vue app loads and its `onMounted` hook
   calls `/api/whoami` — same-origin, cookie attached automatically.
4. Open the browser's network tab (or just look at the rendered `<pre>`):
   the response is the backend's echo of the request HOG actually sent it.

## What the backend receives

For a request through `/api/...`, `stripPrefix: /api` removes the prefix
before proxying, and HOG rewrites the request before it reaches
`http://backend:9000`:

- **`X-User-Id`** — always present; the session subject. Never spoofable —
  HOG strips any inbound `X-User-*` header before injecting its own.
- **`X-User-Email`, `X-User-Name`, `X-User-Given-Name`, `X-User-Family-Name`**
  — one header per passport claim. This example uses the default claim set;
  configure a different set with `identity.claims` on the `Gateway` spec.
- **No `X-User-Groups`** — only present if `identity.groups` is configured,
  which it isn't here (see
  [An API gateway with authorization](quickstart-api-gateway.md) for that).
- **`Authorization: Bearer <access_token>`** — only because this route sets
  `forwardAccessToken: true`. Any client-supplied `Authorization` header is
  always removed first, so this is the only way a bearer token reaches the
  backend.
- **No `Cookie` header** — stripped by default (`forwardCookies: false`),
  so the backend never sees `hog_session`.

## Configuration notes

- **`session.key`** must be exactly 32 bytes (AES-256); `session.ttl`
  defaults to `8h` if omitted.
- **The OIDC env vars and `redirectURL`** — `issuer`, `clientID`,
  `clientSecret`, and `redirectURL` are all required for `kind: IdP`. The
  redirect URL's exact string (scheme, host, port, path) must match what's
  registered at the provider, or the authorization request is rejected
  before HOG ever sees a callback.
- **`SameSite=Lax`**, not `Strict` — the session cookie needs to survive
  the redirect chain back from the IdP, which `Strict` would block.
- **`/auth/*` auto-mounts** — `/auth/login`, `/auth/logout`,
  `/auth/session`, and the callback path derived from `redirectURL` are all
  wired up automatically once both `session` and an `IdP` are configured.
  There's exactly one active `IdP` resource per config.
- **A public shell, gated API** — if you'd rather the SPA shell always
  load (so it can show its own "sign in" state) and only the API redirect
  to `401`, set `access: { auth: public }` on the `app` route and leave
  `auth: required` only on `api`. The SPA then reacts to a `401` from
  `/api/...` itself, e.g. by redirecting to `/auth/login?return_to=...`
  from JavaScript.

## Next steps

- Gate individual routes by group membership:
  [An API gateway with authorization](quickstart-api-gateway.md).
- The same login flow, explained in more depth:
  [A BFF with OIDC login](bff-oidc.md).
- Full session/identity/IdP field list: the
  [configuration reference](../operations/configuration.md) and
  [configure authentication](../operations/authentication.md).
