package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemStoreGetSetDelete(t *testing.T) {
	s := newMemStore()
	ctx := context.Background()

	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("missing key want ErrStateNotFound, got %v", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("after delete want ErrStateNotFound, got %v", err)
	}
}

func TestMemStoreTTLExpiry(t *testing.T) {
	s := newMemStore()
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), -time.Second); err != nil { // already expired
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expired want ErrStateNotFound, got %v", err)
	}
}

type memEntry struct {
	val []byte
	exp time.Time // zero ⇒ no expiry
}

// memStore is an in-memory StateStore for tests only — never registered in production.
type memStore struct {
	mu sync.Mutex
	m  map[string]memEntry
}

func newMemStore() *memStore { return &memStore{m: map[string]memEntry{}} }

func (s *memStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok || (!e.exp.IsZero() && time.Now().After(e.exp)) {
		return nil, ErrStateNotFound
	}
	return e.val, nil
}

func (s *memStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exp time.Time
	if ttl != 0 {
		exp = time.Now().Add(ttl)
	}
	s.m[key] = memEntry{val: append([]byte(nil), value...), exp: exp}
	return nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

var _ StateStore = (*memStore)(nil)
