# Rendering config with kustomize

HOG's config resources (`Gateway`, `Route`, `RouteGroup`, `Policy`, `IdP`,
`Telemetry`, …) are deliberately Kubernetes-*style*: every resource has
`apiVersion` / `kind` / `metadata` / `spec`, the same envelope
[`kubectl`](https://kubernetes.io/docs/reference/kubectl/) and
[`kustomize`](https://kubectl.docs.kubernetes.io/references/kustomize/)
already know how to find, identify, and patch — even though these are *not*
real Kubernetes API objects and never reach a cluster's API server. That
means you can manage a base config plus any number of per-environment
overlays with the exact same tool you already use for Kubernetes manifests,
without HOG needing to know anything about kustomize at all: you run
`kustomize build` yourself (or in CI, or in a Dockerfile — see
[below](#kustomize-in-the-hog-builder-image)) and hand HOG the *rendered*
output.

## `apiVersion` is required for this to work

HOG itself never reads or validates `apiVersion` — every example elsewhere
in these docs could drop it and HOG wouldn't notice. But kustomize uses
`apiVersion` + `kind` + `metadata.name` (+ `metadata.namespace`, unused
here) as a resource's identity, and it skips any YAML document that doesn't
look like a resource at all. So every resource you want kustomize to manage
needs an `apiVersion`, even a made-up one:

```yaml
apiVersion: hog.dev/v1
kind: Gateway
metadata:
  name: hog
spec:
  listen: ":8080"
```

`hog.dev/v1` isn't a registered/versioned API in any schema registry — it's
just a stable, unambiguous string. Pick one and keep it consistent across
your resources.

## Layout: base + overlays

The standard kustomize shape works unchanged:

```
config/
├── base/
│   ├── gateway.yaml
│   ├── route.yaml
│   └── kustomization.yaml       # resources: [gateway.yaml, route.yaml]
└── overlays/
    ├── staging/
    │   └── kustomization.yaml   # resources: [../../base], patches: [...]
    └── prod/
        └── kustomization.yaml   # resources: [../../base], patches: [...]
```

`base/` holds the resources that are identical across every environment.
Each `overlays/<env>/kustomization.yaml` references the base and layers on
the fields that differ — upstream hostnames, trusted-proxy CIDRs, replica
counts if you template your Deployment alongside this, etc.

## Use JSON6902 patches, not strategic-merge

Kustomize supports several patch styles. For HOG resources, use **JSON6902**
(`patches:` with an inline `op`/`path`/`value` list) or **`replacements`** —
not strategic-merge patches (a bare partial resource under `patches:` with
only the fields you want to change, which kustomize deep-merges by key).

Strategic-merge relies on OpenAPI schema annotations (`x-kubernetes-patch-strategy`,
`x-kubernetes-patch-merge-key`) to know whether a list field should be
merged element-by-element or replaced wholesale. Kubernetes' built-in types
ship that schema; HOG's custom kinds publish none, so kustomize falls back
to a generic merge that treats every list (`trustedProxies`, `backends`,
`scopes`, …) as *replace the whole list*, and offers no way to patch a
single named list element (e.g. one `backends[].upstream` among several)
predictably. JSON6902's explicit `path` (a JSON Pointer) and `replacements`'
explicit source/target field paths don't depend on any schema at all — they
say exactly which field to touch, so they behave the same for a `Route` as
for a `Deployment`.

## A complete worked example

**`config/base/gateway.yaml`:**

```yaml
apiVersion: hog.dev/v1
kind: Gateway
metadata:
  name: hog
spec:
  listen: ":8080"
  trustedProxies:
    - 10.0.0.0/8
```

**`config/base/route.yaml`:**

```yaml
apiVersion: hog.dev/v1
kind: Route
metadata:
  name: account
spec:
  match: /account/
  handler:
    type: reverse-proxy
    upstream: http://account-svc.dev.svc.cluster.local:9000
    stripPrefix: /account
  access:
    auth: required
```

**`config/base/kustomization.yaml`:**

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - gateway.yaml
  - route.yaml
```

**`config/overlays/prod/kustomization.yaml`** patches the backend's
`upstream` to the prod service address and widens `trustedProxies` to the
prod cluster's pod CIDR:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base

patches:
  - target:
      kind: Route
      name: account
    patch: |-
      - op: replace
        path: /spec/handler/upstream
        value: http://account-svc.prod.svc.cluster.local:9000
  - target:
      kind: Gateway
      name: hog
    patch: |-
      - op: replace
        path: /spec/trustedProxies
        value: [10.1.0.0/16]
```

Render it:

```sh
kustomize build config/overlays/prod
```

This is real output from that exact layout, using the local `kustomize`
binary (`kustomize version` → `v5.8.1` at the time this was written)
— no placeholders:

```yaml
apiVersion: hog.dev/v1
kind: Gateway
metadata:
  name: hog
spec:
  listen: :8080
  trustedProxies:
  - 10.1.0.0/16
---
apiVersion: hog.dev/v1
kind: Route
metadata:
  name: account
spec:
  access:
    auth: required
  handler:
    stripPrefix: /account
    type: reverse-proxy
    upstream: http://account-svc.prod.svc.cluster.local:9000
  match: /account/
```

Two things to notice: kustomize re-serializes YAML (keys sorted
alphabetically, `:8080` unquoted) — both are semantically identical to the
base and parse the same for HOG — and only the two patched fields
(`spec.trustedProxies` and `spec.handler.upstream`) changed; everything
else passed through from the base untouched.

Pipe the rendered output straight into a file HOG (or `hog-build`) reads:

```sh
kustomize build config/overlays/prod > /etc/hog/gateway.yaml
hog --config /etc/hog/gateway.yaml
```

## `kustomize` in the `hog-builder` image

The `hog-builder` image (used to compose a [custom binary](../developer/building-binaries.md)
with `hog-build`) ships `kustomize` alongside the Go toolchain, so a
Dockerfile build stage can render an overlay and hand the result straight to
`hog-build` without adding another base image or installing kustomize
yourself. See
[Building binaries: rendering config with kustomize](../developer/building-binaries.md#rendering-config-with-kustomize)
for the Dockerfile pattern.

## See also

- [Configuration reference](configuration.md) — every resource kind and
  field kustomize might patch, including the
  [complete annotated example](configuration.md#complete-example) used as
  the source for the base/overlay pattern above.
- [Building binaries](../developer/building-binaries.md) — the plugin
  manifest and `hog-build` CLI this config ultimately feeds.
