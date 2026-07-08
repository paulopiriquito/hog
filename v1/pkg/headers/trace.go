package headers

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Trace format constants
const (
	TraceFormatOTEL    = "otel"
	TraceFormatDatadog = "datadog"
	TraceFormatBoth    = "both"
)

// W3C Trace Context headers
const (
	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
)

// Datadog trace headers
const (
	DatadogTraceIDHeader          = "x-datadog-trace-id"
	DatadogParentIDHeader         = "x-datadog-parent-id"
	DatadogSamplingPriorityHeader = "x-datadog-sampling-priority"
	DatadogOriginHeader           = "x-datadog-origin"
)

// TraceInfo contains extracted trace information
type TraceInfo struct {
	TraceID       string // 128-bit hex (OTEL format)
	SpanID        string // 64-bit hex (OTEL format)
	DDTraceID     string // 64-bit decimal (Datadog format - lower 64 bits of trace ID)
	DDSpanID      string // 64-bit decimal (Datadog format)
	Sampled       bool
	HasValidTrace bool
}

// ExtractTraceContext extracts trace context from incoming request headers
// Supports both W3C traceparent and Datadog headers
func ExtractTraceContext(req *http.Request) context.Context {
	ctx := req.Context()

	// Use OTEL propagator to extract W3C trace context
	propagator := otel.GetTextMapPropagator()
	ctx = propagator.Extract(ctx, propagation.HeaderCarrier(req.Header))

	// If no W3C trace context, try Datadog headers
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		ctx = extractDatadogContext(ctx, req)
	}

	return ctx
}

// extractDatadogContext attempts to extract trace context from Datadog headers
func extractDatadogContext(ctx context.Context, req *http.Request) context.Context {
	ddTraceID := req.Header.Get(DatadogTraceIDHeader)
	ddParentID := req.Header.Get(DatadogParentIDHeader)

	if ddTraceID == "" || ddParentID == "" {
		return ctx
	}

	// Convert Datadog decimal IDs to OTEL hex format
	traceIDUint, err := strconv.ParseUint(ddTraceID, 10, 64)
	if err != nil {
		return ctx
	}

	spanIDUint, err := strconv.ParseUint(ddParentID, 10, 64)
	if err != nil {
		return ctx
	}

	// Create trace ID (128-bit, with lower 64 bits set)
	var traceIDBytes [16]byte
	binary.BigEndian.PutUint64(traceIDBytes[8:], traceIDUint)
	traceID := trace.TraceID(traceIDBytes)

	// Create span ID (64-bit)
	var spanIDBytes [8]byte
	binary.BigEndian.PutUint64(spanIDBytes[:], spanIDUint)
	spanID := trace.SpanID(spanIDBytes)

	// Check sampling priority
	sampled := true
	if samplingPriority := req.Header.Get(DatadogSamplingPriorityHeader); samplingPriority != "" {
		if priority, err := strconv.Atoi(samplingPriority); err == nil {
			sampled = priority > 0
		}
	}

	// Build span context
	var traceFlags trace.TraceFlags
	if sampled {
		traceFlags = trace.FlagsSampled
	}

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: traceFlags,
		Remote:     true,
	})

	return trace.ContextWithRemoteSpanContext(ctx, spanCtx)
}

// InjectTraceHeaders injects trace headers into outgoing request based on format
func InjectTraceHeaders(ctx context.Context, req *http.Request, format string) {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return
	}

	switch format {
	case TraceFormatOTEL:
		injectW3CHeaders(ctx, req)
	case TraceFormatDatadog:
		injectDatadogHeaders(spanCtx, req)
	case TraceFormatBoth:
		injectW3CHeaders(ctx, req)
		injectDatadogHeaders(spanCtx, req)
	default:
		// Default to OTEL
		injectW3CHeaders(ctx, req)
	}
}

// injectW3CHeaders injects W3C traceparent and tracestate headers
func injectW3CHeaders(ctx context.Context, req *http.Request) {
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// injectDatadogHeaders injects Datadog trace headers
func injectDatadogHeaders(spanCtx trace.SpanContext, req *http.Request) {
	traceID := spanCtx.TraceID()
	spanID := spanCtx.SpanID()

	// Convert to Datadog format (64-bit decimal)
	// Use lower 64 bits of trace ID for Datadog compatibility
	ddTraceID := binary.BigEndian.Uint64(traceID[8:])
	ddSpanID := binary.BigEndian.Uint64(spanID[:])

	req.Header.Set(DatadogTraceIDHeader, strconv.FormatUint(ddTraceID, 10))
	req.Header.Set(DatadogParentIDHeader, strconv.FormatUint(ddSpanID, 10))

	// Set sampling priority
	if spanCtx.IsSampled() {
		req.Header.Set(DatadogSamplingPriorityHeader, "1")
	} else {
		req.Header.Set(DatadogSamplingPriorityHeader, "0")
	}
}

// EnsureRootSpan creates a root span if none exists in the context
// Returns the context with span and the span itself
func EnsureRootSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		// Return existing span from context if available
		span := trace.SpanFromContext(ctx)
		if span.SpanContext().IsValid() {
			return ctx, span
		}
		// Context has remote span context but no local span - continue parent
		tracer := otel.Tracer("hog-gateway")
		return tracer.Start(ctx, operationName)
	}

	// No valid trace context - create new root span
	tracer := otel.Tracer("hog-gateway")
	return tracer.Start(ctx, operationName)
}

// GetTraceInfo extracts trace information from context in both formats
func GetTraceInfo(ctx context.Context) TraceInfo {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return TraceInfo{HasValidTrace: false}
	}

	traceID := spanCtx.TraceID()
	spanID := spanCtx.SpanID()

	// OTEL format (hex)
	traceIDHex := traceID.String()
	spanIDHex := spanID.String()

	// Datadog format (decimal, using lower 64 bits of trace ID)
	ddTraceID := binary.BigEndian.Uint64(traceID[8:])
	ddSpanID := binary.BigEndian.Uint64(spanID[:])

	return TraceInfo{
		TraceID:       traceIDHex,
		SpanID:        spanIDHex,
		DDTraceID:     strconv.FormatUint(ddTraceID, 10),
		DDSpanID:      strconv.FormatUint(ddSpanID, 10),
		Sampled:       spanCtx.IsSampled(),
		HasValidTrace: true,
	}
}

// ParseTraceparent parses a W3C traceparent header value
func ParseTraceparent(traceparent string) (traceID, spanID string, sampled bool, err error) {
	parts := strings.Split(traceparent, "-")
	if len(parts) < 4 {
		return "", "", false, fmt.Errorf("invalid traceparent format")
	}

	traceID = parts[1]
	spanID = parts[2]
	flags, err := hex.DecodeString(parts[3])
	if err != nil || len(flags) == 0 {
		return "", "", false, fmt.Errorf("invalid trace flags")
	}
	sampled = flags[0]&0x01 == 1

	return traceID, spanID, sampled, nil
}
