package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInstrumentedTransportEmitsClientSpanAndPropagates(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	var gotTraceparent string
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
	}))
	t.Cleanup(be.Close)

	rt := InstrumentedTransport(http.DefaultTransport)
	req, _ := http.NewRequestWithContext(WithBackend(context.Background(), "org"), "GET", be.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if gotTraceparent == "" {
		t.Fatal("backend did not receive a traceparent")
	}
	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no client span recorded")
	}
	if !hasAttr(spans[0].Attributes(), "hog.backend", "org") {
		t.Fatalf("client span missing hog.backend=org: %v", spans[0].Attributes())
	}
}

func TestInstrumentedTransportRedactsQueryFromSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(be.Close)

	rt := InstrumentedTransport(http.DefaultTransport)
	req, _ := http.NewRequest("GET", be.URL+"/x?api_key=SECRET-123&user=alice", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no client span")
	}
	for _, a := range spans[0].Attributes() {
		if string(a.Key) == "url.full" {
			if strings.Contains(a.Value.AsString(), "SECRET-123") || strings.Contains(a.Value.AsString(), "api_key") {
				t.Fatalf("url.full leaked the query: %q", a.Value.AsString())
			}
		}
	}
}

func hasAttr(attrs []attribute.KeyValue, key, val string) bool {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.AsString() == val {
			return true
		}
	}
	return false
}
