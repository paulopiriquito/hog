package chain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// builtin pairs a stable name with its middleware, so the fixed skeleton order
// is introspectable and testable.
type builtin struct {
	name string
	mw   Middleware
}

// Gates supplies real implementations for the reserved skeleton slots
// (session, auth-gate, authz, projection). A nil field keeps the reserved()
// pass-through. forwarded and security are NOT skeleton slots: the app
// applies both as gateway-wide edge layers that wrap the whole handler
// outermost (forwarded outermost, security wrapping otel), not per-route, so
// they cover every surface — routes and the raw auth endpoints alike — with
// one gate instead of a per-route skeleton slot.
type Gates struct {
	Session    Middleware
	AuthGate   Middleware
	Authz      Middleware
	Projection Middleware
}

// Observability supplies the telemetry-built middlewares for the reserved
// observability slots. A nil AccessLog falls back to the default built-in.
type Observability struct {
	AccessLog Middleware // configured, trace-correlated access log
}

// Skeleton returns the fixed, ordered built-in middlewares that bracket every
// route, with the supplied gates filling their reserved slots. Developer plugins
// are appended AFTER this list by the app, so they can never run ahead of these
// gates. recover/request-id are always real; access-log uses obs (or its
// default when nil); session/auth-gate/authz/projection use the supplied
// gates (or reserved() when nil) and are per-route. forwarded and security
// are not part of this skeleton at all: the app applies both gateway-wide as
// outermost wrappers of the whole handler (forwarded outermost, security
// wrapping otel), so they cover every surface — routes and the raw auth
// endpoints alike — with one gate instead of a per-route skeleton slot.
func Skeleton(logger *slog.Logger, gates Gates, obs Observability) []Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	bs := skeleton(logger, gates, obs)
	out := make([]Middleware, len(bs))
	for i, b := range bs {
		out[i] = b.mw
	}
	return out
}

// SkeletonNames returns the built-in names in chain order (outermost first).
func SkeletonNames() []string {
	bs := skeleton(slog.Default(), Gates{}, Observability{})
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.name
	}
	return names
}

func skeleton(logger *slog.Logger, gates Gates, obs Observability) []builtin {
	accessLog := obs.AccessLog
	if accessLog == nil {
		accessLog = accessLogMW(logger)
	}
	return []builtin{
		{"recover", recoverMW(logger)},
		{"request-id", requestIDMW()},
		{"access-log", accessLog},
		{"session", orReserved(gates.Session)},
		{"auth-gate", orReserved(gates.AuthGate)},
		{"authz", orReserved(gates.Authz)},
		{"projection", orReserved(gates.Projection)},
	}
}

// orReserved returns m, or a reserved() pass-through when m is nil.
func orReserved(m Middleware) Middleware {
	if m == nil {
		return reserved()
	}
	return m
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
					span := trace.SpanFromContext(r.Context())
					span.RecordError(fmt.Errorf("panic: %v", v))
					span.SetStatus(codes.Error, "panic")
					sc := span.SpanContext()
					logger.Error("panic recovered",
						"panic", v, "path", r.URL.Path, "stack", string(debug.Stack()),
						"trace_id", traceIDOrEmpty(sc), "span_id", spanIDOrEmpty(sc))
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

// Unwrap lets http.ResponseController reach the underlying writer so the
// reverse-proxy's FlushInterval:-1 streaming and websocket Hijack pass through
// the default access log fallback.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func traceIDOrEmpty(sc trace.SpanContext) string {
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

func spanIDOrEmpty(sc trace.SpanContext) string {
	if sc.HasSpanID() {
		return sc.SpanID().String()
	}
	return ""
}
