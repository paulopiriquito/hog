package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/terminal"
)

// TestRunSetupAndGracefulShutdown is a lifecycle smoke test: Run should load
// config, set up telemetry (with no otlp.endpoint configured, Setup must not
// error), build and serve the handler, and then shut down cleanly (returning
// nil) once ctx is cancelled — exercising the deferred telemetry flush too.
func TestRunSetupAndGracefulShutdown(t *testing.T) {
	addr := freeAddr(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hog.yaml")
	doc := fmt.Sprintf(`
kind: Gateway
metadata: { name: hog }
spec: { listen: %q }
---
kind: Telemetry
metadata: { name: t }
spec: { service: { name: runtest } }
---
kind: Route
metadata: { name: hc }
spec: { match: /healthz, handler: { type: health } }
`, addr)
	if err := os.WriteFile(cfgPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	terminal.Register(reg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfgPath, reg, nil) }()

	// Give Run a moment to complete telemetry setup and bind the listener,
	// then request graceful shutdown.
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not shut down within 5s")
	}
}

// freeAddr reserves an ephemeral TCP port on loopback and returns its address,
// closing the listener immediately so Run can bind it. This has an inherent
// (tiny) race — another process could grab the port first — but is reliable
// enough for a lifecycle smoke test.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeAddr: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("freeAddr: close: %v", err)
	}
	return addr
}
