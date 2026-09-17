// Package valkeystore implements a session.StateStore backed by Valkey (a
// Redis fork) for HOG's server-side session feature
// (Gateway.spec.stateProvider). It lives in its own Go module so
// github.com/valkey-io/valkey-go never becomes a dependency of the core hog
// module, which is stdlib-first by policy.
//
// Register it in a Gateway resource:
//
//	kind: Gateway
//	spec:
//	  stateProvider:
//	    type: valkey
//	    config:
//	      address: valkey.example.com:6379
//	      user: hog
//	      password: ${VALKEY_PASSWORD}
//	      db: 0
//	      tls: true
//	      timeout: 3s
//
// The store never sees plaintext — HOG seals every record before Set and
// opens it after Get. A record's lifetime is whatever ttl HOG passes to Set
// (the session TTL); this plugin has no TTL setting of its own.
package valkeystore

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/paulopiriquito/hog/v2"
	"github.com/paulopiriquito/hog/v2/config"
	"github.com/paulopiriquito/hog/v2/registry"
	"github.com/paulopiriquito/hog/v2/session"
	"github.com/valkey-io/valkey-go"
)

// Config is the validated `stateProvider.config` block for `type: valkey`.
type Config struct {
	Address  string
	User     string
	Password string
	DB       int
	TLS      bool
	Timeout  time.Duration
}

// String prints only the fields safe to log — never the password.
func (c Config) String() string {
	return fmt.Sprintf("valkeystore.Config{address: %s, user: %s, db: %d, tls: %t, timeout: %s}", c.Address, c.User, c.DB, c.TLS, c.Timeout)
}

// rawConfig mirrors the YAML before defaulting/validation.
type rawConfig struct {
	Address  string `yaml:"address"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	TLS      bool   `yaml:"tls"`
	Timeout  string `yaml:"timeout"`
}

// parseConfig decodes and validates the opaque `config` node. `address` is
// required; `user` defaults to "default" (Valkey's default ACL user); `db`
// defaults to 0 and must not be negative; `timeout` defaults to 3s and must
// parse to a positive duration; `tls` defaults to false.
func parseConfig(raw registry.RawConfig) (Config, error) {
	var rc rawConfig
	if err := raw.Decode(&rc); err != nil {
		return Config{}, fmt.Errorf("valkey: %w", err)
	}
	if rc.Address == "" {
		return Config{}, fmt.Errorf("valkey: address is required")
	}
	if rc.DB < 0 {
		return Config{}, fmt.Errorf("valkey: db must not be negative (got %d)", rc.DB)
	}
	cfg := Config{
		Address:  rc.Address,
		User:     rc.User,
		Password: rc.Password,
		DB:       rc.DB,
		TLS:      rc.TLS,
		Timeout:  3 * time.Second,
	}
	if cfg.User == "" {
		cfg.User = "default"
	}
	if rc.Timeout != "" {
		d, err := time.ParseDuration(rc.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("valkey: timeout: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("valkey: timeout must be positive (got %s)", d)
		}
		cfg.Timeout = d
	}
	return cfg, nil
}

func init() {
	hog.Register(config.KindStateProvider, "valkey", func(name string, raw registry.RawConfig) (any, error) {
		cfg, err := parseConfig(raw)
		if err != nil {
			return nil, fmt.Errorf("stateProvider %q: %w", name, err)
		}
		return New(cfg)
	})
}

// Store is a session.StateStore backed by a single Valkey client. Deliberately
// does not retain cfg.Password (or cfg.User) past construction, so nothing
// beyond the address and database number can ever leak through a log line or
// a %v/%+v format of a *Store.
type Store struct {
	client  valkey.Client
	timeout time.Duration
	address string
	db      int
}

// New dials Valkey and returns a ready Store. The dial failure, if any, is
// wrapped with the address only — never the password.
func New(cfg Config) (*Store, error) {
	opt := valkey.ClientOption{
		InitAddress: []string{cfg.Address},
		Username:    cfg.User,
		Password:    cfg.Password,
		SelectDB:    cfg.DB,
	}
	if cfg.TLS {
		// Managed Valkey endpoints normally present publicly signed certificates,
		// so the system root pool (nil RootCAs) verifies them — no CA file needed
		// here.
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("valkeystore: dial %s: %w", cfg.Address, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Store{client: client, timeout: timeout, address: cfg.Address, db: cfg.DB}, nil
}

// Get returns session.ErrStateNotFound when key is absent or expired.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	b, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, session.ErrStateNotFound
		}
		return nil, fmt.Errorf("valkeystore: get %q: %w", key, err)
	}
	return cloneBytes(b), nil
}

// cloneBytes returns an independent copy of b. AsBytes documents its result
// as an "immutable []byte", but produces it by aliasing the reply's own
// backing array; Get uses this so a caller can never observe that array
// change under it — whether from a future valkey-go version, or from the
// opt-in client-side-caching path (DoCache), which hands the very same
// cached message to every caller. Get uses plain Do, not DoCache, so that
// alias is not reachable through this store today; the copy is deliberately
// defensive against both.
func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Set stores value under key with the given ttl, rounded up to at least 1ms
// (Valkey's PX requires a positive value; ttl is normally the remaining
// session lifetime, which is always positive, but a non-positive ttl is
// guarded against here defensively).
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	ms := ttl.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	cmd := s.client.B().Set().Key(key).Value(string(value)).PxMilliseconds(ms).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkeystore: set %q: %w", key, err)
	}
	return nil
}

// Delete removes key. Deleting an already-absent key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("valkeystore: delete %q: %w", key, err)
	}
	return nil
}

// Close releases the underlying Valkey client's connections.
func (s *Store) Close() { s.client.Close() }

// String prints only the address and database number — never the password.
func (s *Store) String() string {
	return fmt.Sprintf("valkeystore.Store{address: %s, db: %d}", s.address, s.db)
}
