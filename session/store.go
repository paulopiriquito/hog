package session

import (
	"context"
	"errors"
	"time"
)

// ErrStateNotFound is returned by StateStore.Get when the key is absent.
var ErrStateNotFound = errors.New("session: state not found")

// StateStore persists already-encrypted session records by opaque key with a TTL.
// Implementations are developer-provided plugins registered under
// config.KindStateProvider; they NEVER see plaintext — HOG seals/opens the bytes.
// Get must return ErrStateNotFound when the key is absent or expired.
type StateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}
