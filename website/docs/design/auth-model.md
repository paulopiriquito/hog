# Authentication and sessions

HOG is a Backend-for-Frontend (BFF): it is the OpenID Connect *relying
party*, not an identity provider. A frontend developer configures an issuer,
a client ID and secret, and a redirect URL, and HOG runs the entire login
protocol on the browser's behalf — the single-page app never sees a token.

## The browser flow

Login is the OIDC Authorization Code flow with PKCE enabled by default (it
can be turned off for confidential clients that don't need it). HOG issues
`state` and `nonce`, carries them across the redirect in a short-lived,
encrypted `hog_login` cookie, and validates both on return — this keeps the
login round trip stateless and cluster-safe: any replica can complete a
callback a different replica started.

Once the IdP redirects back with a code, HOG exchanges it, verifies the ID
token against the IdP's JWKS, and — only when the configured identity model
needs claims the ID token doesn't carry — fetches userinfo. The result
becomes a **session**, sealed into an encrypted, `HttpOnly` cookie.

## Why `SameSite=Lax`, not `Strict`

The session cookie uses `SameSite=Lax`, not `Strict`. The browser returning
from the IdP's login page to HOG's `/auth/callback` is a top-level,
cross-site navigation, and a `Strict` cookie is withheld on exactly that
request — it would break the redirect back into the app on every login.
`Lax` still sends the cookie on that safe, top-level `GET`, while withholding
it on cross-site subrequests and form submissions, enough to blunt CSRF for
state-changing requests. HOG layers Fetch-metadata based origin checks on
top rather than relying on the cookie attribute alone.

## The session fingerprint

At login, HOG derives a fingerprint from a configurable set of request
attributes — by default just `User-Agent` — as
`base64(sha256(canonical(headers)))`, and seals it inside the session cookie.
Every read recomputes the fingerprint and compares it in constant time; a
mismatch invalidates the session. This is a deliberate trade-off, not true
proof-of-possession: the attributes are spoofable, and a UA string can drift
across a browser auto-update and force a benign re-login. A client-signed
proof-of-possession scheme was considered and rejected because it would
require the frontend to write auth code — violating the "zero frontend auth
code" goal the BFF model exists to deliver. The fingerprint header set is
configurable so operators can tune the trade-off.

## Two views of identity

HOG keeps two identity shapes. **`PublicView`** is what the SPA sees from
the session-info endpoint — subject, passport claims, groups, expiry — never
tokens or the fingerprint. **`Principal`** is the server-side view on the
request context, read by the authorization gate, the reverse proxy, and
plugins; it carries the access token but never the refresh token or
fingerprint, which stay inside the session-lifecycle code alone. Nothing
downstream of the session gate touches raw session bytes.

The principal's subject defaults to the token's own `sub`, but
`identity.subjectClaim` can point it at any claim instead, on both the
cookie and the Bearer paths. This is what makes a token that carries no
`sub` at all — as some providers issue for access tokens — usable as an
identity source: the subject just comes from wherever that provider does
put it.

## API Bearer auth: the non-browser path

Routes typed `service` also accept `Authorization: Bearer <jwt>` — a token a
non-browser client obtained directly from the IdP, typically via client
credentials. HOG verifies it offline against the IdP's cached JWKS (signature,
issuer, expiry, and audience — configurable, defaulting to the client ID) and
projects it into the same `Principal` shape the cookie flow produces, so
downstream code can't tell which path resolved the request.

Not every provider signs access tokens the same way it signs ID tokens.
That mismatch is what the `bearer` block exists for: with certain
providers, access tokens verify against a key set of their own
(`bearer.jwksURL`, or the `jwks_access_token_uri` a discovery document can
advertise separately from `jwks_uri`), and they carry no `sub` and no
standard `aud` at all — the client identity lives in a claim like
`client_id` instead, checked via `bearer.audienceClaim`. HOG treats an
access token without `sub` as valid rather than rejecting it outright,
provided `identity.subjectClaim` names a claim the token does carry.

Resolution is cookie-first — a valid session takes precedence — and Bearer
is accepted only on service routes, never on app (SPA) routes, since
browsers never send `Authorization` automatically and so present no CSRF
surface there.

The claims that make up a user's passport and how groups are derived are
configured once, in a shared identity model used by both paths. That
separation is what lets HOG run as a pure, cookieless API gateway — Bearer
only, no session cookie, no login endpoints — using the same
identity-projection rules a full BFF deployment uses.

## Pluggable session state

The session cookie's contents depend on which state provider is configured:

- **Cookie-self-contained (default).** The full session — passport, groups,
  access token, expiry, fingerprint — is sealed with AES-256-GCM directly in
  the cookie, auto-chunked across numbered cookies if it grows too large for
  one. No server-side state: any replica decrypts any session with the shared
  key, keeping the cluster coordination-free. The trade-off is that a refresh
  token is too sensitive to ever hold client-side, so it's discarded at
  login — this mode has **no silent refresh**; an expired session requires
  re-login.
- **Delegated (opt-in).** The cookie holds only an opaque session ID; the
  full record — including the refresh token — lives in an external store
  keyed by that ID, encrypted by HOG before it ever reaches the store. This
  unlocks silent refresh: HOG quietly renews the access token as it nears
  expiry. The core module defines a minimal key-value-with-TTL interface
  (`session.StateStore`) and stays dependency-free; a store is a plugin
  living in its own module. HOG ships one, `statestore-valkey`
  (Valkey-backed), as the reference implementation and a ready-to-use
  default — a developer can plug in another store the same way.

Both modes share the same `Manager` interface, so nothing above the session
layer needs to know which one is active.

## Identity projection to backends

Before a request reaches a backend, HOG strips every inbound `X-User-*`
header — unconditionally, even unauthenticated — so a client can never
smuggle identity past the gateway. If a `Principal` was resolved, HOG then
injects its own: `X-User-Id` always, one header per persisted passport
claim, and a groups header (default `X-User-Groups`) if groups are
configured. This strip-then-inject order is what makes the header set
trustworthy for backends to consume directly.

Backends never receive HOG's own session or login cookies by default (the
`Cookie` header is stripped before proxying, with an explicit opt-in to pass
it through) or the access token. The token is injected as `Authorization:
Bearer <token>` only when a route explicitly enables it — off by default,
never logged — because forwarding it hands a backend the ability to call
further upstream services as the user, a capability that should be a
deliberate choice, not a default.

## The identity assertion

`X-User-*` headers work for one hop: HOG projects a trustworthy identity
onto the request it forwards, but that identity doesn't survive a second
HOG instance sitting further down the chain, because a plain header is
exactly what the previous section says never to trust from an inbound
request. The identity assertion exists to cross that second hop without
falling back to a plain, forgeable header, and without asking the
downstream instance to re-run userinfo against the IdP for an identity the
upstream instance already resolved.

Rather than trust a header, the upstream instance signs one: a compact,
Ed25519-signed JWS naming the subject, passport, and groups it resolved,
minted fresh per request with a short lifetime (60 seconds by default) and
carried in its own header, never the caller's session or access token. The
downstream instance verifies the signature and issuer against a configured
key before trusting anything in it, and, by default, only lets a valid
assertion *enrich* a principal a Bearer token has already authenticated on
that same request — it does not treat the header as a credential on its
own unless an operator explicitly opts into that.

That default is the load-bearing decision: an assertion is deliberately
weaker proof than the things HOG already verifies cryptographically end to
end (an ID token against the IdP's JWKS, a session cookie against HOG's own
seal). It carries no audience and no unique id, so any instance configured
with the matching issuer and key accepts it, and a captured token replays
for the rest of its lifetime plus clock-skew tolerance. Accepting one is
inherently trusting the issuing instance's own resolution of the principal,
not independently re-deriving it — a relationship between two deployments
you operate and have chosen to connect this way, not an independent
authentication check. Binding it to an already-authenticated principal by
default keeps that trust relationship from becoming, by itself, a way to
authenticate a request that reached the downstream instance directly.

See [operations: authentication](../operations/authentication.md) for
configuration details.
