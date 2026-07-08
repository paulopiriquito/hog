package session

import "sync"

// keyedMutex provides a per-key mutex with reference-counted cleanup, so
// concurrent operations on the same key serialize without leaking map entries.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*refMutex
}

type refMutex struct {
	mu   sync.Mutex
	refs int
}

// lock acquires the mutex for key and returns an unlock function.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*refMutex{}
	}
	rm := k.m[key]
	if rm == nil {
		rm = &refMutex{}
		k.m[key] = rm
	}
	rm.refs++
	k.mu.Unlock()

	rm.mu.Lock()
	return func() {
		rm.mu.Unlock()
		k.mu.Lock()
		rm.refs--
		if rm.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
