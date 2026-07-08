# Testing plugins

Test a plugin at two levels: a fast unit test of the factory and handler in
isolation, and a slower integration test that composes and runs the actual
binary. HOG's own built-in modules follow the same split — mirror it.

## Unit test: factory + handler

A terminal handler's factory takes a `registry.RawConfig` and returns
something implementing `http.Handler` (see [Writing plugins](writing-plugins.md)).
Test both steps together: build a `yaml.Node` from a YAML string, wrap it in a
`registry.RawConfig`, build the module, then drive the resulting handler with
`net/http/httptest`. Because your plugin's own `init()` already registered it
on `registry.Default` by the time the test binary runs (the test file lives
in the same package as the plugin), you can build through the registry
directly rather than calling an unexported constructor:

```go
package greeter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"gopkg.in/yaml.v3"
)

func TestGreeterHandler(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("type: greeter\nmessage: hi\n"), &node); err != nil {
		t.Fatal(err)
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = *node.Content[0] // unwrap the document node yaml.Unmarshal produces
	}

	m, err := registry.Default.Build(config.KindTerminalHandler, "greeter", registry.RawConfig{Node: node})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	h, ok := m.(http.Handler)
	if !ok {
		t.Fatalf("greeter module is not an http.Handler: %T", m)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello", nil))

	if got := rec.Body.String(); got != "hi\n" {
		t.Errorf("body = %q, want %q", got, "hi\n")
	}
}
```

Also test the factory's error paths directly — a missing required field, an
invalid value — by building with a `RawConfig` that omits or corrupts them,
and asserting `Build` returns an error. Since the factory should fail fast at
build time (see [Writing plugins](writing-plugins.md#a-complete-terminal-handler-plugin)),
these are cheap tests that catch a misconfigured deployment before it ships.

If your handler touches shared state across requests — a cache, a counter, a
connection pool — run its tests with the race detector:

```sh
go test -race ./...
```

## Integration test: the composed binary

A unit test proves your handler works in isolation; it doesn't prove the
binary actually compiles with your plugin in it and serves the route you
configured. For that, build a real binary with `hog-build --replace` pointing
at your local module, start it, and hit the route over HTTP — the same
pattern HOG's own test suite uses in `internal/hogbuild/build_test.go` to
verify plugin composition end to end. Because `internal/hogbuild` isn't
importable outside the `hog` module, a plugin repo shells out to the
`hog-build` binary instead:

```go
func TestComposedBinaryServesRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: runs the go toolchain")
	}
	work := t.TempDir()

	// Pick a free port so the test doesn't collide with a running instance.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	cfg := filepath.Join(work, "gateway.yaml")
	os.WriteFile(cfg, []byte(`
kind: Gateway
metadata: { name: hog }
spec:
  listen: "`+addr+`"
  plugins:
    - github.com/acme/hog-greeter
---
kind: Route
metadata: { name: hello }
spec: { match: /hello, handler: { type: greeter }, access: { auth: public } }
`), 0o600)

	bin := filepath.Join(work, "hog")
	build := exec.Command("hog-build", "--config", cfg, "-o", bin,
		"--replace", "github.com/acme/hog-greeter=.")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("hog-build: %v", err)
	}

	cmd := exec.Command(bin, "--config", cfg)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// Poll: the binary needs a moment to bind its listener.
	var body string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/hello")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if body != "hello\n" { // greeter's default message — the route config above sets no override
		t.Fatalf("route body = %q, want %q (plugin not compiled in?)", body, "hello\n")
	}
}
```

`hog-build` must be on `PATH` and `--hog-source` (or `$HOG_SOURCE`) must point
at the `hog` module source — see
[Building a custom binary](building-binaries.md) for both flags. Gate this
test behind `testing.Short()` as shown: it drives the real `go` toolchain, so
it's slow enough to skip from a fast unit-test loop and run only in CI or
before a release.

## Running everything

```sh
go test ./...          # unit tests
go test -race ./...    # unit tests, with the race detector
go test -run Integration -v .   # (if you name integration tests distinctly)
```
