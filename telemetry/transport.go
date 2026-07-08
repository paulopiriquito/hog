package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type backendKey struct{}

// WithBackend tags ctx with the low-cardinality backend name (upstream host or
// api group) used to label the outbound client span/metrics.
func WithBackend(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, backendKey{}, name)
}

func backendName(ctx context.Context) string {
	n, _ := ctx.Value(backendKey{}).(string)
	return n
}

// InstrumentedTransport wraps base with otelhttp so backend calls emit client
// spans + http.client.* metrics and propagate W3C traceparent, tagged with the
// low-cardinality hog.backend name. The exported span's url.full has its query
// string redacted (a client-forwarded query may carry secrets, e.g. ?api_key=…,
// and HOG already strips Cookie/Authorization to backends by the same posture).
// It binds the OTel GLOBAL (delegating) providers, so it may be built before
// Setup runs.
func InstrumentedTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	// inner runs INSIDE otelhttp's client span (otelhttp starts the span and
	// injects it into the request context before calling inner), so it can tag
	// the span and override otelhttp's own url.full attribute.
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		span := trace.SpanFromContext(r.Context())
		if n := backendName(r.Context()); n != "" {
			span.SetAttributes(attribute.String("hog.backend", n))
		}
		// Redact the query string from url.full: reverse-proxy always forwards the
		// inbound query and api may (forwardQuery), so a client-supplied query
		// secret (?api_key=…) must not be exported to the telemetry collector.
		// HOG already strips Cookie/Authorization to backends by the same posture.
		if r.URL != nil && r.URL.RawQuery != "" {
			u := *r.URL
			u.RawQuery = ""
			span.SetAttributes(semconv.URLFull(u.String()))
		}
		return base.RoundTrip(r)
	})
	return otelhttp.NewTransport(inner,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if n := backendName(r.Context()); n != "" {
				return "backend " + n
			}
			return r.Method
		}),
		otelhttp.WithMetricAttributesFn(func(r *http.Request) []attribute.KeyValue {
			if n := backendName(r.Context()); n != "" {
				return []attribute.KeyValue{attribute.String("hog.backend", n)}
			}
			return nil
		}),
	)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
