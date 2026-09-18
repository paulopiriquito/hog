# Releases

Release notes and upgrade guidance for HOG.

- [**HOG v2.2.0**](v2.2.0.md) — an `IdP` can declare `verificationOnly`, so
  an instance that only verifies access tokens someone else issued needs
  neither a client secret nor a redirect URL.
- [**HOG v2.1.1**](v2.1.1.md) — applies `bearer.signingAlgs` on every
  verifier path, refuses `forwardIdentity` without an assertion issuer,
  replaces a working signing seed in the example configuration, and
  repairs the repository links the module rename broke.
- [**HOG v2.1.0**](v2.1.0.md) — access tokens from a dedicated key set, a
  configurable subject claim, group prefix strip, an authorization
  deny-redirect, an identity assertion between HOG instances, and a shipped
  Valkey session store.
- [**HOG v2.0.0**](v2.0.0.md) — the clean-room, native-Go rewrite. HOG is no longer
  a fork of KrakenD.
- [**Migrating from v1**](migrating-from-v1.md) — what changed, the v1 deprecation
  timeline, and how to move a v1 deployment to v2.
