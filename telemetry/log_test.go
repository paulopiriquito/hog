package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestLogAttrsWithSpanContext(t *testing.T) {
	tid, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	attrs := LogAttrs(ctx)
	got := map[string]string{}
	for _, a := range attrs {
		got[string(a.Key)] = a.Value.String()
	}
	if got["trace_id"] != tid.String() {
		t.Fatalf("trace_id = %q, want %q", got["trace_id"], tid.String())
	}
	if got["span_id"] != sid.String() {
		t.Fatalf("span_id = %q, want %q", got["span_id"], sid.String())
	}
}

func TestLogAttrsEmptyContext(t *testing.T) {
	attrs := LogAttrs(context.Background())
	if len(attrs) != 0 {
		t.Fatalf("attrs = %+v, want empty for a context with no span", attrs)
	}
}
