# Scaling and availability

HOG is stateless by design: there is no leader election, no shared control
plane, and no in-memory state that a request depends on having hit a
particular replica before. Scale it the same way you'd scale any stateless
HTTP service.

## Horizontal scaling

Run as many replicas as you want behind a load balancer. Any replica can
serve any request — including completing an OIDC login flow a different
replica started, since the transient login state (`state`, `nonce`, PKCE
verifier) travels in a short-lived, encrypted `hog_login` cookie, not
server-side memory.

Rolling updates are safe: because there's no coordination between
instances, an old and a new replica can serve traffic side by side during a
deploy with no compatibility handshake required, as long as the config
(especially `session.key`) doesn't change out from under a running session.

## Session state

How much a request depends on shared state is entirely a function of
`Gateway.spec.stateProvider`:

- **Cookie-self-contained (default — no `stateProvider`).** The whole
  session — passport, groups, access token, expiry, fingerprint — is
  sealed with AES-256-GCM directly in the cookie. Any replica holding the
  same `session.key` can decrypt any session; there is no server-side
  session store to keep in sync, replicate, or fail over. The trade-off:
  the refresh token is never stored (it's too sensitive to hold
  client-side), so there is **no silent access-token refresh** — an
  expired session simply requires re-login.

- **Delegated (opt-in, `stateProvider` configured).** The cookie holds only
  an opaque session ID; the full record — including the refresh token —
  lives in an external store you provide, encrypted by HOG before it's
  written. This unlocks silent refresh: HOG renews the access token behind
  the scenes as it nears expiry (`stateProvider.refreshSkew`, default
  `60s`), single-flighted per session within one process and tolerant of a
  benign double-refresh across instances. A store implements a small
  `Get`/`Set`/`Delete`-with-TTL interface (`session.StateStore`) as a
  plugin, registered under a `type` name — HOG ships one such plugin,
  `github.com/paulopiriquito/hog/plugins/statestore-valkey`, backed by
  [Valkey](https://valkey.io):

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
  plugins:
    - github.com/paulopiriquito/hog/plugins/statestore-valkey@v1.0.0
  session:
    key: ${SESSION_KEY}
  stateProvider:
    type: valkey
    refreshSkew: 60s
    config:
      address: valkey.example.com:6379
      user: hog
      password: ${VALKEY_PASSWORD}
      db: 0
      tls: true
      timeout: 3s
```

`plugins` compiles the module into the binary via
[`hog-build`](../developer/building-binaries.md); `stateProvider.config`'s
`address`, `user`, `password`, `db`, `tls`, and `timeout` are that plugin's
own fields — the plugin has no TTL setting of its own, since a record's
lifetime is always whatever `ttl` HOG's session manager passes to `Set`
(`Gateway.spec.session.ttl`), not something the store configures
independently. A different store is still a small interface to implement —
see the plugin's own module for a worked example.

Choosing delegated state means your session store (not HOG) becomes the
thing that needs its own availability story — size and replicate it the way
you would for any shared cache. See
[developer: writing plugins](../developer/writing-plugins.md) to implement
one, and [configuration: `Gateway.stateProvider`](configuration.md#gateway-stateprovider)
for the field reference.

## No shared control plane

Every HOG instance loads its own copy of the config at startup from
`--config` and never phones home to any other instance. There is nothing
like a control-plane API, a gossip protocol, or a shared cache that
instances must agree on to serve correctly — consistency across replicas is
achieved simply by giving every instance the same config and the same
`session.key`, which any config-distribution mechanism you already use
(a baked image, a mounted `ConfigMap`, etc.) already gives you.

## Health checks

HOG has no mandatory built-in health endpoint — mount one explicitly with
the `health` terminal handler, which always returns `200
{"status":"ok"}` and needs no config:

```yaml
kind: Route
metadata: { name: healthz, labels: { tier: system } }
spec:
  match: /healthz
  handler: { type: health }
  access: { auth: public }
```

Point your load balancer's/orchestrator's liveness and readiness probes at
this path. Because it's just a route, you can label it and put it behind
whatever authorization policy fits your environment (typically `public`, so
the probe doesn't need credentials).
