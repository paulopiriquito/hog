// Package chain composes the per-route middleware chain: a fixed built-in
// skeleton plus the two guarded developer slots.
package chain

import "slices"

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
	for _, mw := range slices.Backward(mws) {
		h = mw.Wrap(h)
	}
	return h
}
