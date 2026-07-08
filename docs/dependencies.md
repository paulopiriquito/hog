# Dependencies

HOG keeps a small, focused dependency set. Direct runtime dependencies (from `go.mod`):

| Module | Used for | Where |
|---|---|---|
| github.com/coreos/go-oidc/v3 | OIDC discovery + ID-token verification | `idp/` (`idp/oidc.go`) |
| github.com/go-jose/go-jose/v4 | JOSE/JWT primitives for test fixtures only (signing fake ID tokens / JWKS) | test-only: `app/build_test.go`, `idp/fake_test.go` — **not imported by any non-test runtime code** |
| github.com/open-policy-agent/opa | Embedded Rego policy engine (Tier B authorization) | `authz/` (`authz/engine.go` only — see package doc in `authz/rule.go`) |
| golang.org/x/oauth2 | OAuth2 Authorization Code exchange | `auth/` (`auth/endpoints.go`), `idp/` (`idp/oidc.go`) |
| go.opentelemetry.io/otel (+ sdk, otlp exporters, otelhttp contrib) | Opt-in traces/metrics over OTLP, W3C propagation, HTTP instrumentation, span attributes | `telemetry/`, `app/build.go`, `authz/gate.go`, `chain/builtin.go` |
| gopkg.in/yaml.v3 | Config decoding (Kubernetes-style YAML resources) | `app/`, `auth/`, `authz/`, `config/`, `gateway/`, `registry/`, `route/`, `security/`, `session/` |

Everything else in `go.sum` is an indirect dependency pulled in by the above (notably OPA's
Rego/JSON stack — `lestrrat-go/*`, `gobwas/glob`, `valyala/fastjson`, etc. — and the
OpenTelemetry SDK/gRPC/protobuf stack). Test tooling (`chromedp`, `testify`) lives only in the
separate `tests/e2e` module and never enters the runtime build.

## Note: go-jose is test-only

`github.com/go-jose/go-jose/v4` is declared as a direct (non-indirect) dependency in `go.mod`
because Go's module graph counts test-file imports, but a repo-wide, non-test grep
(`git grep -l "go-jose" -- '*.go' ':!v1/' ':!*_test.go'`) finds zero hits. The only imports are in
`app/build_test.go` and `idp/fake_test.go`, where it signs fake ID tokens/JWKS for test fixtures.
Actual runtime OIDC token verification goes through `github.com/coreos/go-oidc/v3`, which vendors
its own JOSE handling internally. `go-jose` is therefore a candidate for removal from the runtime
module (e.g. moving those fixtures into `tests/e2e`, or vendoring a smaller stub) if minimizing the
direct dependency set further is a goal — it is not currently exercised by any code path that ships
in the runtime binary.
