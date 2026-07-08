# Deployment topology

HOG is designed to run as N identical, stateless replicas behind a single
TLS-terminating load balancer or ingress — the same coordination-free cluster
model KrakenD popularized. Any replica can answer any request; there is no
leader election and no required shared control plane.

## Behind a trusted proxy

HOG speaks plain HTTP; it never terminates TLS itself. It derives the
request's external scheme, host, and client IP from `X-Forwarded-Proto`,
`X-Forwarded-Host`, and `X-Forwarded-For` — values that only a fronting proxy
can set. These drive the OIDC redirect URI, the session cookie's `Secure`
attribute, and the `X-Forwarded-*` chain HOG forwards on to backends. The
`Gateway` resource's `trustedProxies` field is **enforced**: a gateway-wide
`forwarded` layer (the outermost wrapper around the whole handler, applied
before request routing and before OpenTelemetry) strips `X-Forwarded-For`,
`X-Forwarded-Proto`, `X-Forwarded-Host`, `X-Forwarded-Port`, `X-Real-Ip`, and
`Forwarded` from any request whose immediate peer isn't in `trustedProxies`.
`trustedProxies` accepts CIDRs and bare IPs; `"*"` trusts every peer; the
default — an empty list — trusts **no** peer, so those headers are stripped
from every request unless you configure it (the secure default). In practice
this means: with no `trustedProxies` set, HOG only ever sees its own
direct-connection peer address, session cookies fall back to always-`Secure`
(since the `X-Forwarded-Proto` override that would otherwise permit a
non-`https` cookie is stripped), and the logged/projected client IP is always
the immediate peer, never the true client — set `trustedProxies` to your load
balancer's/ingress's CIDR to see the real client IP and scheme. Still run HOG
directly behind that proxy, with no untrusted hop in a position to spoof its
own forwarded headers — `trustedProxies` establishes *which* peer is
trusted, not that every hop before it is.

## Horizontal scaling and session state

Because there is no sticky-session requirement, replicas scale out and back
freely behind the load balancer. What differs by configuration is *where the
session lives*:

- **Default — stateless cookie.** With no `stateProvider` configured, the
  entire session (passport, groups, access token, expiry, fingerprint) is
  sealed into an encrypted, `HttpOnly` cookie — chunked across numbered
  cookies if it grows large. Any replica that holds the shared `session.key`
  can decrypt any request's cookie; there is no server-side session store and
  no silent refresh (the caller re-authenticates when the session expires).
- **Opt-in — server-side state provider.** When the `Gateway`'s
  `stateProvider` block is set, the cookie holds only an opaque session ID;
  the sealed record — including the refresh token, which is never sent to the
  client — lives in a `StateStore` module keyed by that ID. This unlocks
  silent refresh of the access token near expiry. HOG core ships no storage
  backend for this: the `StateStore` interface is a minimal encrypted
  KV-with-TTL contract, and an operator registers their own implementation
  (for example, a Valkey-backed one) as a plugin. A store error fails closed
  — the session becomes invalid rather than trusting an unreadable record.

Either way, the cluster stays coordination-free: adding a state provider adds
a shared backend for sessions, not a shared control plane for the gateway
itself.

## Container images

HOG ships as a small set of Docker build stages:

- **`hog-runtime`** — the base runtime image: a `golang:1.26-alpine` build
  stage compiles `cmd/hog`, and the runtime stage is a minimal Alpine image
  running as a non-root user (`hog`, uid/gid `10001`). It exposes port
  `8080` — matching the `Gateway`'s default `listen` address — and starts as
  `hog --config /etc/hog`. Being fully stateless, it is designed to run with
  `docker run --read-only --tmpfs /tmp`.
- **`hog-static`** — `hog-runtime` preconfigured with a default `Gateway`
  config that serves `/srv/web` as a single-page app; operators build from it
  and copy their compiled frontend into `/srv/web`.
- **`hog-builder`** — a build-stage-only image carrying the HOG source plus
  the `hog-build` composer, used to produce a custom binary from a plugin
  manifest (see [extensibility](extensibility.md)).

See [installation & images](../operations/installation.md) for the full
image reference and Dockerfile patterns.

## Listen port and observability export

HOG exposes a single plain-HTTP listener at `gateway.listen` (default
`:8080`) — there is no separate metrics or admin port. Traces and metrics are
pushed via OTLP (`http/protobuf` or `gRPC`) to a collector endpoint set in the
`Telemetry` resource's `otlp.endpoint`. With no endpoint configured, W3C trace
propagation and trace/span ID allocation still happen in-process — so every
log line, including a panic caught by `recover`, still carries correlation
IDs — but nothing is exported off-box.
