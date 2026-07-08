# Developer guide

This section is for extending HOG, not just configuring it: embedding it in
your own Go program, writing plugins, and building custom binaries. If you
only need to configure the built-in modules, see the
[operations guide](../operations/index.md) instead.

There are two ways to extend HOG. Both compile down to the same artifact — a
single static Go binary — and both rely on the same compile-time
[module registry](../overview/concepts.md#plugins-the-registry):

- **Import HOG as a Go framework.** Write your own `main` package, blank-import
  your plugin packages, and call `hog.Main()`. You control the module and the
  build. See [Framework mode](framework-mode.md).
- **Write plugins and let `hog-build` compose the binary.** List your plugin
  import paths in the `Gateway` resource's `plugins` manifest and run the
  `hog-build` CLI to generate and compile the binary for you. See
  [Building a custom binary](building-binaries.md).

Either way, the code you write is the same: a Go package whose `init()`
registers a factory. Start with [Writing plugins](writing-plugins.md).

## Chapters

- [Framework mode](framework-mode.md) — embed HOG in your own `main`, and when
  to prefer this over `hog-build`.
- [Writing plugins](writing-plugins.md) — the `registry.Factory` contract,
  decoding config, and a complete terminal handler plugin.
- [Building a custom binary](building-binaries.md) — the plugin manifest, the
  `hog-build` CLI, and the two-stage Docker image family.
- [Testing plugins](testing.md) — unit-testing a factory and handler, and
  integration-testing a composed binary.
- [Contributing](contributing.md) — repo layout and conventions for changes to
  HOG itself.

For the concepts referenced throughout — resources, routes, terminals, the
middleware chain — see [core concepts](../overview/concepts.md). For how the
registry fits into the request lifecycle, see
[architecture: extensibility](../architecture/extensibility.md).
