package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LogAttrs returns trace_id/span_id slog attrs for ctx (empty attrs omitted), so
// application log sites under a request correlate to the trace.
func LogAttrs(ctx context.Context) []slog.Attr {
	sc := trace.SpanContextFromContext(ctx)
	var a []slog.Attr
	if sc.HasTraceID() {
		a = append(a, slog.String("trace_id", sc.TraceID().String()))
	}
	if sc.HasSpanID() {
		a = append(a, slog.String("span_id", sc.SpanID().String()))
	}
	return a
}
