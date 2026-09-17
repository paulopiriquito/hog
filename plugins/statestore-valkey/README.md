# statestore-valkey

A `session.StateStore` implementation backed by [Valkey](https://valkey.io) (a
Redis fork), for HOG's server-side session feature
(`Gateway.spec.stateProvider`). See
[writing plugins](https://paulopiriquito.github.io/hog/developer/writing-plugins/)
for the general plugin contract this fills.

It lives in its own Go module (`github.com/paulopiriquito/hog/plugins/statestore-valkey`)
so `github.com/valkey-io/valkey-go` never becomes a dependency of the core `hog`
module, which is stdlib-first by policy.

## Including it in a build

List it in the `Gateway` resource's plugin manifest and let
[`hog-build`](https://paulopiriquito.github.io/hog/developer/building-binaries/)
compile it in:

```yaml
kind: Gateway
metadata: { name: my-gateway }
spec:
  listen: ":8080"
  plugins:
    - github.com/paulopiriquito/hog/plugins/statestore-valkey@v1.0.0
  stateProvider:
    type: valkey
    config:
      address: valkey.example.com:6379
      user: hog
      password: ${VALKEY_PASSWORD}
      db: 0
      tls: true
      timeout: 3s
```

```sh
hog-build --config gateway.yaml -o ./hog
```

This plugin is a separate Go module with its own version line, so its tag does not
follow the gateway's. Pin the plugin version you want; any `v1` of it works with any
HOG `v2` gateway, and `hog-build` resolves the two independently.

While iterating locally, point `hog-build` at your working copy instead of a
tagged version with `--replace`:

```sh
hog-build --config gateway.yaml -o ./hog \
  --replace github.com/paulopiriquito/hog/plugins/statestore-valkey=./plugins/statestore-valkey
```

In framework mode, blank-import the package next to `hog.Main()` instead.

## Configuration

`stateProvider.config` fields:

| Field      | Type     | Default     | Notes                                                    |
|------------|----------|-------------|-----------------------------------------------------------|
| `address`  | string   | *required*  | `host:port` of the Valkey instance.                        |
| `user`     | string   | `default`   | ACL username; `default` is Valkey's built-in user.         |
| `password` | string   | `""`        | Never appears in an error, a log line, or a `%v`/`%+v` of the store. |
| `db`       | int      | `0`         | Logical database index (`SELECT`); must not be negative.   |
| `tls`      | bool     | `false`     | See below.                                                 |
| `timeout`  | duration | `3s`        | Per-operation timeout (`Get`/`Set`/`Delete`); must parse to a positive duration. |

## TLS

The default, `tls: false`, connects in plaintext: the ACL password above, and
every key and value this store reads or writes, cross the wire unencrypted.
Only leave it unset on a network you trust (a private VPC, or a loopback
connection) — anything else should set `tls: true`.

Set `tls: true` for a TLS-terminated Valkey endpoint. The client verifies the
server certificate against the system root pool — a managed Valkey endpoint
normally presents a publicly signed certificate, so no CA file needs to be
shipped alongside the binary. `MinVersion` is pinned to TLS 1.2.

## Record lifetime

This plugin has no TTL setting of its own: every record's lifetime is
whatever `ttl` HOG's session manager passes to `Set` — the session TTL
(`Gateway.spec.session.ttl`), not something configured here.

## Testing

`go vet ./... && go test ./...` runs the unit tests unconditionally. The
round-trip integration test (`TestStoreRoundTrip`) is skipped unless
`VALKEY_ADDRESS` is set to a reachable `host:port`:

```sh
VALKEY_ADDRESS=localhost:6379 go test ./... -run TestStoreRoundTrip -v
```

From the repository root, `make plugins-test` runs that same `go vet` + `go test`
for this module. It is a deliberate, standalone target rather than a
prerequisite of the root `make ci`: fetching this module's dependencies
(starting with `github.com/valkey-io/valkey-go`) needs network access that
`make ci` must not require to stay runnable offline. The CI workflow runs
`make plugins-test` as its own step, where that access is available.
