package chain

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// builtin pairs a stable name with its middleware, so the fixed skeleton order
// is introspectable and testable.
type builtin struct {
	name string
	mw   Middleware
}

// Skeleton returns the fixed, ordered built-in middlewares that bracket every
// route. Developer plugins are appended AFTER this list by the app, so they can
// never run ahead of these gates. recover/request-id/access-log are real; the
// security…projection gates are faithful reserved pass-throughs that later
// specs replace with real logic at these exact positions.
func Skeleton(logger *slog.Logger) []Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	bs := skeleton(logger)
	out := make([]Middleware, len(bs))
	for i, b := range bs {
		out[i] = b.mw
	}
	return out
}

// SkeletonNames returns the built-in names in chain order (outermost first).
func SkeletonNames() []string {
	bs := skeleton(slog.Default())
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.name
	}
	return names
}

func skeleton(logger *slog.Logger) []builtin {
	return []builtin{
		{"recover", recoverMW(logger)},
		{"request-id", requestIDMW()},
		{"access-log", accessLogMW(logger)},
		{"security", reserved()},   // CSRF + headers — implemented in BFF/security spec
		{"session", reserved()},    // identity resolution — auth spec
		{"auth-gate", reserved()},  // 302 vs 401 — auth spec
		{"authz", reserved()},      // OPA — authz spec
		{"projection", reserved()}, // X-User-* / claims — auth spec
	}
}

// reserved is a faithful no-op holding a fixed chain position for a later spec.
func reserved() Middleware {
	return Func(func(next http.Handler) http.Handler { return next })
}

func recoverMW(logger *slog.Logger) Middleware {
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					logger.Error("panic recovered",
						"panic", v,
						"path", r.URL.Path,
						"stack", string(debug.Stack()),
					)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	})
}

func requestIDMW() Middleware {
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				var b [16]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r)
		})
	})
}

func accessLogMW(logger *slog.Logger) Middleware {
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// status is pre-set to 200: if the handler calls Write without WriteHeader,
			// net/http sends 200 implicitly and our WriteHeader override is never invoked.
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("access",
				"method", r.Method, "path", r.URL.Path,
				"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
		})
	})
}

// statusWriter records the status code for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
