package gologging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/paulopiriquito/hog/pkg/headers"
)

// Context key for trace info
type traceContextKey struct{}

// RequestTraceInfo stores trace information for the current request
type RequestTraceInfo struct {
	TraceID   string
	SpanID    string
	DDTraceID string
	DDSpanID  string
}

// goroutine-local storage for trace info (fallback when context is not available)
var (
	currentTraceInfo *RequestTraceInfo
	traceInfoMutex   sync.RWMutex
)

// SetCurrentTraceInfo sets the trace info for the current goroutine
// This is used as a fallback when context is not passed through the call chain
func SetCurrentTraceInfo(info *RequestTraceInfo) {
	traceInfoMutex.Lock()
	defer traceInfoMutex.Unlock()
	currentTraceInfo = info
}

// GetCurrentTraceInfo gets the trace info for the current goroutine
func GetCurrentTraceInfo() *RequestTraceInfo {
	traceInfoMutex.RLock()
	defer traceInfoMutex.RUnlock()
	return currentTraceInfo
}

// ClearCurrentTraceInfo clears the trace info
func ClearCurrentTraceInfo() {
	traceInfoMutex.Lock()
	defer traceInfoMutex.Unlock()
	currentTraceInfo = nil
}

// ContextWithTraceInfo adds trace info to context
func ContextWithTraceInfo(ctx context.Context, info *RequestTraceInfo) context.Context {
	return context.WithValue(ctx, traceContextKey{}, info)
}

// TraceInfoFromContext extracts trace info from context
func TraceInfoFromContext(ctx context.Context) *RequestTraceInfo {
	if info, ok := ctx.Value(traceContextKey{}).(*RequestTraceInfo); ok {
		return info
	}
	return nil
}

// FormatLogMessageWithTrace appends trace info to a log message based on config
func FormatLogMessageWithTrace(message string) string {
	cfg := GetActiveConfig()
	if cfg == nil {
		return message
	}

	info := GetCurrentTraceInfo()
	if info == nil {
		return message
	}

	if cfg.Format == "json" {
		return message // JSON formatting is handled differently
	}

	// For text format, append trace IDs
	switch cfg.TraceFormat {
	case TraceFormatOTEL:
		if info.TraceID != "" {
			return fmt.Sprintf("%s trace_id=%s span_id=%s", message, info.TraceID, info.SpanID)
		}
	case TraceFormatDatadog:
		if info.DDTraceID != "" {
			return fmt.Sprintf("%s dd.trace_id=%s dd.span_id=%s", message, info.DDTraceID, info.DDSpanID)
		}
	case TraceFormatBoth:
		if info.TraceID != "" {
			return fmt.Sprintf("%s trace_id=%s span_id=%s dd.trace_id=%s dd.span_id=%s",
				message, info.TraceID, info.SpanID, info.DDTraceID, info.DDSpanID)
		}
	}

	return message
}

// EnrichJSONLogWithTrace adds trace fields to a JSON log map
func EnrichJSONLogWithTrace(logMap map[string]interface{}) {
	cfg := GetActiveConfig()
	if cfg == nil {
		return
	}

	info := GetCurrentTraceInfo()
	if info == nil {
		return
	}

	switch cfg.TraceFormat {
	case TraceFormatOTEL:
		if info.TraceID != "" {
			logMap["trace_id"] = info.TraceID
			logMap["span_id"] = info.SpanID
		}
	case TraceFormatDatadog:
		if info.DDTraceID != "" {
			logMap["dd.trace_id"] = info.DDTraceID
			logMap["dd.span_id"] = info.DDSpanID
		}
	case TraceFormatBoth:
		if info.TraceID != "" {
			logMap["trace_id"] = info.TraceID
			logMap["span_id"] = info.SpanID
			logMap["dd.trace_id"] = info.DDTraceID
			logMap["dd.span_id"] = info.DDSpanID
		}
	}
}

// BuildJSONLogEntry creates a complete JSON log entry with trace info
func BuildJSONLogEntry(level, module, message string) string {
	cfg := GetActiveConfig()

	logMap := map[string]interface{}{
		"level":   level,
		"module":  module,
		"message": message,
	}

	// Add trace info
	EnrichJSONLogWithTrace(logMap)

	// Add custom tags
	if cfg != nil {
		for k, v := range cfg.Tags {
			logMap[k] = v
		}
	}

	jsonBytes, err := json.Marshal(logMap)
	if err != nil {
		return message
	}

	return string(jsonBytes)
}

// ExtractAndStoreTraceInfo extracts trace info from headers.TraceInfo and stores it
func ExtractAndStoreTraceInfo(traceInfo headers.TraceInfo) *RequestTraceInfo {
	if !traceInfo.HasValidTrace {
		return nil
	}

	info := &RequestTraceInfo{
		TraceID:   traceInfo.TraceID,
		SpanID:    traceInfo.SpanID,
		DDTraceID: traceInfo.DDTraceID,
		DDSpanID:  traceInfo.DDSpanID,
	}

	SetCurrentTraceInfo(info)
	return info
}
