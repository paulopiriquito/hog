package telemetry

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/session"
	"go.opentelemetry.io/otel/trace"
)

// AccessLog builds the configured, trace-correlated access-log middleware. It
// emits one "access" record per request at cfg.Level through logger (whose base
// attrs already include `service`); raising the logger's level above cfg.Level
// suppresses it.
func AccessLog(cfg AccessLogConfig, logger *slog.Logger) chain.Middleware {
	level := cfg.SlogLevel()
	props := cfg.Properties
	headers := cfg.Headers
	fields := cfg.Fields
	return chain.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &accessRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			if !logger.Enabled(r.Context(), level) {
				return
			}
			attrs := make([]slog.Attr, 0, len(props)+len(headers)+len(fields))
			for _, p := range props {
				if a, ok := accessLogProperty(p, r, rw, start); ok {
					attrs = append(attrs, a)
				}
			}
			for field, header := range headers {
				if v := r.Header.Get(header); v != "" {
					attrs = append(attrs, slog.String(field, v))
				}
			}
			for field, v := range fields {
				attrs = append(attrs, slog.String(field, v))
			}
			logger.LogAttrs(r.Context(), level, "access", attrs...)
		})
	})
}

// accessLogProperty resolves one built-in access-log property to an slog attr.
func accessLogProperty(name string, r *http.Request, rw *accessRecorder, start time.Time) (slog.Attr, bool) {
	sc := trace.SpanContextFromContext(r.Context())
	switch name {
	case "method":
		return slog.String("method", r.Method), true
	case "path":
		return slog.String("path", r.URL.Path), true
	case "route":
		return slog.String("route", r.Pattern), true
	case "query":
		return slog.String("query", redactQuery(r.URL.RawQuery)), true
	case "status":
		return slog.Int("status", rw.status), true
	case "duration_ms":
		return slog.Int64("duration_ms", time.Since(start).Milliseconds()), true
	case "bytes_out":
		return slog.Int("bytes_out", rw.bytes), true
	case "client_ip":
		return slog.String("client_ip", accessLogClientIP(r)), true
	case "host":
		return slog.String("host", r.Host), true
	case "protocol":
		return slog.String("protocol", r.Proto), true
	case "user_id":
		if p, ok := session.FromContext(r.Context()); ok {
			return slog.String("user_id", p.Subject), true
		}
		return slog.Attr{}, false
	case "session_id":
		if p, ok := session.FromContext(r.Context()); ok && p.SessionID != "" {
			return slog.String("session_id", p.SessionID), true
		}
		return slog.Attr{}, false
	case "request_id":
		if id := rw.Header().Get("X-Request-Id"); id != "" {
			return slog.String("request_id", id), true
		}
		return slog.Attr{}, false
	case "trace_id":
		if sc.HasTraceID() {
			return slog.String("trace_id", sc.TraceID().String()), true
		}
		return slog.Attr{}, false
	case "span_id":
		if sc.HasSpanID() {
			return slog.String("span_id", sc.SpanID().String()), true
		}
		return slog.Attr{}, false
	}
	return slog.Attr{}, false
}

func accessLogClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sensitiveParams are query keys whose values are redacted in the access log.
var sensitiveParams = map[string]bool{
	"code": true, "state": true, "token": true, "access_token": true, "id_token": true,
	"refresh_token": true, "api_key": true, "apikey": true, "client_secret": true,
	"password": true, "assertion": true,
}

// redactQuery replaces the values of sensitive query params with "REDACTED",
// preserving keys and non-sensitive params. On a parse error it returns "" (fail
// closed — never echo an unparseable raw query that might carry a secret).
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	for k := range vals {
		if sensitiveParams[strings.ToLower(k)] {
			vals[k] = []string{"REDACTED"}
		}
	}
	return vals.Encode()
}

// accessRecorder captures status + bytes for the access log.
type accessRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rc *accessRecorder) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *accessRecorder) Write(b []byte) (int, error) {
	n, err := rc.ResponseWriter.Write(b)
	rc.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer so the
// reverse-proxy's FlushInterval:-1 streaming and websocket Hijack pass through
// the access log.
func (rc *accessRecorder) Unwrap() http.ResponseWriter { return rc.ResponseWriter }
