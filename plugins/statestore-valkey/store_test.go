package valkeystore

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/v2/config"
	"github.com/paulopiriquito/hog/v2/registry"
	"github.com/paulopiriquito/hog/v2/session"
	"gopkg.in/yaml.v3"
)

// rawConfigFromYAML builds a registry.RawConfig from a YAML block, the same
// way the app decodes a `stateProvider.config` node before handing it to a
// plugin factory.
func rawConfigFromYAML(t *testing.T, s string) registry.RawConfig {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return registry.RawConfig{Node: *n.Content[0]}
	}
	return registry.RawConfig{Node: n}
}

func TestParseConfigDefaultsAndValidation(t *testing.T) {
	t.Run("address required", func(t *testing.T) {
		if _, err := parseConfig(rawConfigFromYAML(t, "user: bob\n")); err == nil {
			t.Fatal("want error when address is missing")
		}
	})

	t.Run("defaults", func(t *testing.T) {
		cfg, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\n"))
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if cfg.Address != "localhost:6379" {
			t.Fatalf("address = %q", cfg.Address)
		}
		if cfg.User != "default" {
			t.Fatalf("user default = %q, want %q", cfg.User, "default")
		}
		if cfg.DB != 0 {
			t.Fatalf("db default = %d, want 0", cfg.DB)
		}
		if cfg.Timeout != 3*time.Second {
			t.Fatalf("timeout default = %v, want 3s", cfg.Timeout)
		}
		if cfg.TLS {
			t.Fatal("tls default = true, want false")
		}
	})

	t.Run("user override", func(t *testing.T) {
		cfg, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\nuser: bob\n"))
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if cfg.User != "bob" {
			t.Fatalf("user = %q, want %q", cfg.User, "bob")
		}
	})

	t.Run("negative db rejected", func(t *testing.T) {
		if _, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ndb: -1\n")); err == nil {
			t.Fatal("want error for negative db")
		}
	})

	t.Run("db override", func(t *testing.T) {
		cfg, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ndb: 2\n"))
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if cfg.DB != 2 {
			t.Fatalf("db = %d, want 2", cfg.DB)
		}
	})

	t.Run("unparseable timeout rejected", func(t *testing.T) {
		if _, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ntimeout: not-a-duration\n")); err == nil {
			t.Fatal("want error for unparseable timeout")
		}
	})

	t.Run("non-positive timeout rejected", func(t *testing.T) {
		if _, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ntimeout: 0s\n")); err == nil {
			t.Fatal("want error for zero timeout")
		}
		if _, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ntimeout: -1s\n")); err == nil {
			t.Fatal("want error for negative timeout")
		}
	})

	t.Run("timeout override", func(t *testing.T) {
		cfg, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ntimeout: 500ms\n"))
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if cfg.Timeout != 500*time.Millisecond {
			t.Fatalf("timeout = %v, want 500ms", cfg.Timeout)
		}
	})

	t.Run("tls override", func(t *testing.T) {
		cfg, err := parseConfig(rawConfigFromYAML(t, "address: localhost:6379\ntls: true\n"))
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if !cfg.TLS {
			t.Fatal("tls = false, want true")
		}
	})
}

// var _ session.StateStore ensures *Store keeps satisfying the interface HOG
// core defines — a compile-time check, not a runtime test.
var _ session.StateStore = (*Store)(nil)

// TestCloneBytesReturnsIndependentMemory proves cloneBytes — the helper Get
// uses on every reply — returns memory that is genuinely independent of its
// argument, not merely a re-slice of it. It fails immediately if cloneBytes
// is weakened to return its argument unchanged: the backing-array check
// catches an aliased zero-copy return, and the post-copy mutation catches a
// copy that only covers part of the buffer.
//
// This does not, on its own, prove that Get's call to cloneBytes cannot be
// bypassed — see the package doc comment on cloneBytes and the report for
// why that end-to-end aliasing cannot be forced from a black-box test
// against the valkey-go client (its ValkeyResult/ValkeyMessage types are
// unexported with no test-construction hook, and the library's current blob
// string decoder already allocates a fresh buffer per reply, so there is no
// reachable window today where Get's result would visibly change out from
// under the caller, live server or not).
func TestCloneBytesReturnsIndependentMemory(t *testing.T) {
	src := []byte("sealed-record-bytes")
	got := cloneBytes(src)

	if string(got) != string(src) {
		t.Fatalf("cloneBytes(%q) = %q, want an equal-content copy", src, got)
	}
	if len(got) > 0 && &got[0] == &src[0] {
		t.Fatal("cloneBytes returned the same backing array as its argument")
	}

	// Mutate the source after the copy: if cloneBytes aliased src instead of
	// copying it, this would change got too.
	want := string(got)
	for i := range src {
		src[i] = 'X'
	}
	if string(got) != want {
		t.Fatalf("mutating the source changed the copy: got %q, want unaffected %q", got, want)
	}
}

// TestFactoryRegisteredAndInvalidConfigNamesInstance checks two things without
// requiring a live Valkey server: the "valkey" factory is actually registered
// under config.KindStateProvider (an unregistered kind/name pair fails with a
// distinct "no module" error from registry.Build itself), and building it
// with an invalid config surfaces an error naming the instance. registry.Build
// passes the registered name ("valkey") to the factory as instanceName.
func TestFactoryRegisteredAndInvalidConfigNamesInstance(t *testing.T) {
	built, err := registry.Default.Build(config.KindStateProvider, "valkey", rawConfigFromYAML(t, "user: bob\n"))
	if err == nil {
		t.Fatalf("want error for missing address, got store %v", built)
	}
	if strings.Contains(err.Error(), "no module") {
		t.Fatalf("valkey factory does not appear to be registered: %v", err)
	}
	if !strings.Contains(err.Error(), `"valkey"`) {
		t.Fatalf("error %q does not name the instance", err.Error())
	}
}

func TestStoreRoundTrip(t *testing.T) {
	addr := os.Getenv("VALKEY_ADDRESS")
	if addr == "" {
		t.Skip("VALKEY_ADDRESS not set; skipping integration test")
	}

	cfg := Config{Address: addr, User: "default", Timeout: 3 * time.Second}
	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(store.Close)

	ctx := t.Context()
	key := fmt.Sprintf("hog:test:%d", time.Now().UnixNano())

	t.Run("missing key", func(t *testing.T) {
		if _, err := store.Get(ctx, key); !errors.Is(err, session.ErrStateNotFound) {
			t.Fatalf("Get missing key: err = %v, want ErrStateNotFound", err)
		}
	})

	t.Run("set then get", func(t *testing.T) {
		want := []byte("sealed-record-bytes")
		if err := store.Set(ctx, key, want, time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("Get = %q, want %q", got, want)
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, session.ErrStateNotFound) {
			t.Fatalf("Get after delete: err = %v, want ErrStateNotFound", err)
		}
	})

	t.Run("ttl expiry", func(t *testing.T) {
		ttlKey := key + ":ttl"
		if err := store.Set(ctx, ttlKey, []byte("short-lived"), time.Second); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)
		if _, err := store.Get(ctx, ttlKey); !errors.Is(err, session.ErrStateNotFound) {
			t.Fatalf("Get after ttl expiry: err = %v, want ErrStateNotFound", err)
		}
	})
}
