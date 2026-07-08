package gologging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luraproject/lura/v2/logging"
	"github.com/paulopiriquito/hog/pkg/headers"
)

// AccessLogEntry represents a structured access log entry
type AccessLogEntry struct {
	Timestamp string  `json:"timestamp"`
	Level     string  `json:"level"`
	Module    string  `json:"module"`
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Query     string  `json:"query,omitempty"`
	Status    int     `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
	ClientIP  string  `json:"client_ip"`
	UserAgent string  `json:"user_agent"`
	Host      string  `json:"host"`
	RequestID string  `json:"request_id,omitempty"`
	// OTEL trace fields
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
	// Datadog trace fields
	DDTraceID string `json:"dd.trace_id,omitempty"`
	DDSpanID  string `json:"dd.span_id,omitempty"`
}

// AccessLogger creates a Gin middleware for structured access logging
// This should be added after TraceContextMiddleware
func AccessLogger(logger logging.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if access logging is enabled
		if !IsAccessLogEnabled() {
			c.Next()
			return
		}

		// Check if path should be skipped
		if ShouldSkipPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Record start time
		start := time.Now()

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Get trace info from the current context (set by TraceContextMiddleware)
		traceInfo := headers.GetTraceInfo(c.Request.Context())

		// Build log entry based on format
		cfg := GetActiveConfig()

		if cfg.Format == "json" {
			logAccessJSON(logger, c, latency, traceInfo, cfg)
		} else {
			logAccessText(logger, c, latency, traceInfo, cfg)
		}
	}
}

// logAccessJSON outputs access log in JSON format
func logAccessJSON(logger logging.Logger, c *gin.Context, latency time.Duration, traceInfo headers.TraceInfo, cfg *Config) {
	entry := AccessLogEntry{
		Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:     "INFO",
		Module:    "ACCESS",
		Method:    c.Request.Method,
		Path:      c.Request.URL.Path,
		Query:     c.Request.URL.RawQuery,
		Status:    c.Writer.Status(),
		LatencyMs: float64(latency.Microseconds()) / 1000.0,
		ClientIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Host:      c.Request.Host,
	}

	// Add trace IDs based on format
	if traceInfo.HasValidTrace {
		switch cfg.TraceFormat {
		case TraceFormatOTEL:
			entry.TraceID = traceInfo.TraceID
			entry.SpanID = traceInfo.SpanID
		case TraceFormatDatadog:
			entry.DDTraceID = traceInfo.DDTraceID
			entry.DDSpanID = traceInfo.DDSpanID
		case TraceFormatBoth:
			entry.TraceID = traceInfo.TraceID
			entry.SpanID = traceInfo.SpanID
			entry.DDTraceID = traceInfo.DDTraceID
			entry.DDSpanID = traceInfo.DDSpanID
		default:
			entry.TraceID = traceInfo.TraceID
			entry.SpanID = traceInfo.SpanID
		}
	}

	// Add custom tags to JSON output
	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		logger.Error("[ACCESS] Failed to marshal access log:", err)
		return
	}

	// Merge custom tags into JSON
	if len(cfg.Tags) > 0 {
		var jsonMap map[string]interface{}
		json.Unmarshal(jsonBytes, &jsonMap)
		for k, v := range cfg.Tags {
			jsonMap[k] = v
		}
		jsonBytes, _ = json.Marshal(jsonMap)
	}

	// Output as raw JSON (logger.Info would add its own formatting)
	fmt.Println(string(jsonBytes))
}

// logAccessText outputs access log in text format
func logAccessText(logger logging.Logger, c *gin.Context, latency time.Duration, traceInfo headers.TraceInfo, cfg *Config) {
	traceStr := ""
	if traceInfo.HasValidTrace {
		switch cfg.TraceFormat {
		case TraceFormatOTEL:
			traceStr = fmt.Sprintf(" trace_id=%s span_id=%s", traceInfo.TraceID, traceInfo.SpanID)
		case TraceFormatDatadog:
			traceStr = fmt.Sprintf(" dd.trace_id=%s dd.span_id=%s", traceInfo.DDTraceID, traceInfo.DDSpanID)
		case TraceFormatBoth:
			traceStr = fmt.Sprintf(" trace_id=%s span_id=%s dd.trace_id=%s dd.span_id=%s",
				traceInfo.TraceID, traceInfo.SpanID, traceInfo.DDTraceID, traceInfo.DDSpanID)
		default:
			traceStr = fmt.Sprintf(" trace_id=%s span_id=%s", traceInfo.TraceID, traceInfo.SpanID)
		}
	}

	tagsStr := ""
	for k, v := range cfg.Tags {
		tagsStr += fmt.Sprintf(` %s="%s"`, k, v)
	}

	logger.Info(fmt.Sprintf("[ACCESS] %s %s %d %v %s %s%s%s",
		c.Request.Method,
		c.Request.URL.Path,
		c.Writer.Status(),
		latency,
		c.ClientIP(),
		c.Request.UserAgent(),
		traceStr,
		tagsStr,
	))
}

// TraceContextMiddleware extracts trace context and ensures a root span exists
// This should be added early in the middleware chain
func TraceContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract trace context from incoming headers
		ctx := headers.ExtractTraceContext(c.Request)

		// Ensure we have a root span for this request
		ctx, span := headers.EnsureRootSpan(ctx, fmt.Sprintf("HTTP %s %s", c.Request.Method, c.Request.URL.Path))
		defer span.End()

		// Update request with enriched context
		c.Request = c.Request.WithContext(ctx)

		// Get trace info and store it for other loggers
		traceInfo := headers.GetTraceInfo(ctx)
		ExtractAndStoreTraceInfo(traceInfo)
		defer ClearCurrentTraceInfo()

		c.Next()
	}
}
