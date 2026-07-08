package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupNoEndpointInstallsPropagatorAndTracer(t *testing.T) {
	shutdown, err := Setup(context.Background(), Default(), nil)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	fields := otel.GetTextMapPropagator().Fields()
	if !contains(fields, "traceparent") {
		t.Fatalf("propagator fields = %v (want traceparent)", fields)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	if !span.SpanContext().HasTraceID() || !span.SpanContext().HasSpanID() {
		t.Fatal("span has no trace/span id")
	}
	span.End()
}

func TestSetupHonoursInboundSampledDecision(t *testing.T) {
	shutdown, err := Setup(context.Background(), Default(), nil) // no endpoint ⇒ NeverSample root
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x01}, SpanID: trace.SpanID{0x01}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), remote)
	_, span := otel.Tracer("t").Start(ctx, "child")
	if !span.SpanContext().IsSampled() {
		t.Fatal("inbound sampled decision not honoured")
	}
	span.End()
}

func TestSetupInvalidProtocol(t *testing.T) {
	c := Default()
	c.OTLP.Endpoint = "http://x:4318"
	c.OTLP.Protocol = "nope"
	if _, err := Setup(context.Background(), c, nil); err == nil {
		t.Fatal("want error on invalid protocol")
	}
}

func TestSetupExportingBuildsAndShutsDownWithoutConnecting(t *testing.T) {
	// Setup itself never dials out: the OTLP exporters connect lazily, so
	// construction succeeds even before any server exists. But Shutdown does
	// a final flush that calls Export, so proving it completes cleanly (rather
	// than hanging or erroring) needs *something* listening — a real collector
	// isn't required, so this stands up a local httptest server that just
	// returns 200 (matching the SDK's own client tests, e.g.
	// otlpmetrichttp's client_test.go), instead of pointing at an address
	// that can never succeed (e.g. 127.0.0.1:0, which fails the flush outright).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := Default()
	c.OTLP.Endpoint = srv.URL
	shutdown, err := Setup(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
