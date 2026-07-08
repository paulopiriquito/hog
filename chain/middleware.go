// Package chain composes the per-route middleware chain: a fixed built-in
// skeleton plus the two guarded developer slots.
package chain

import "net/http"

// Middleware wraps a handler, returning a handler.
type Middleware interface {
	Wrap(next http.Handler) http.Handler
}

// Func adapts a plain function to Middleware.
type Func func(http.Handler) http.Handler

// Wrap implements Middleware.
func (f Func) Wrap(next http.Handler) http.Handler { return f(next) }

// Compose wraps terminal with mws so that mws[0] is the OUTERMOST handler
// (runs first on the way in, last on the way out).
func Compose(terminal http.Handler, mws ...Middleware) http.Handler {
	h := terminal
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i].Wrap(h)
	}
	return h
}
