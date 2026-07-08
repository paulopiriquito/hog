package gologging

import (
	"log/syslog"
	"sync"
)

// Global config storage for access by other packages
var (
	activeConfig     *Config
	activeConfigLock sync.RWMutex
)

// Config is the custom config struct containing the params for the logger
type Config struct {
	Level          string
	StdOut         bool
	Syslog         bool
	SysLogFacility syslog.Priority
	SyslogSeverity syslog.Priority
	Prefix         string
	Format         string
	CustomFormat   string
	Tags           map[string]string
	// New fields for access logging and tracing
	AccessLog   bool
	SkipPaths   []string
	TraceFormat string // "otel", "datadog", or "both"
}

// TraceFormat constants
const (
	TraceFormatOTEL    = "otel"
	TraceFormatDatadog = "datadog"
	TraceFormatBoth    = "both"
)

// SetActiveConfig stores the config for access by other packages
func SetActiveConfig(cfg *Config) {
	activeConfigLock.Lock()
	defer activeConfigLock.Unlock()
	activeConfig = cfg
}

// GetActiveConfig returns the current logging configuration
func GetActiveConfig() *Config {
	activeConfigLock.RLock()
	defer activeConfigLock.RUnlock()
	if activeConfig == nil {
		return &Config{
			AccessLog:   true,
			TraceFormat: TraceFormatOTEL,
		}
	}
	return activeConfig
}

// GetActiveFormat returns the current format setting
func GetActiveFormat() string {
	cfg := GetActiveConfig()
	return cfg.Format
}

// IsAccessLogEnabled returns whether access logging is enabled
func IsAccessLogEnabled() bool {
	cfg := GetActiveConfig()
	return cfg.AccessLog
}

// GetSkipPaths returns the paths to skip for access logging
func GetSkipPaths() []string {
	cfg := GetActiveConfig()
	return cfg.SkipPaths
}

// GetTraceFormat returns the trace format setting
func GetTraceFormat() string {
	cfg := GetActiveConfig()
	if cfg.TraceFormat == "" {
		return TraceFormatOTEL
	}
	return cfg.TraceFormat
}

// ShouldSkipPath checks if the given path should be skipped for access logging
func ShouldSkipPath(path string) bool {
	skipPaths := GetSkipPaths()
	for _, skipPath := range skipPaths {
		if path == skipPath {
			return true
		}
		// Support prefix matching with trailing *
		if len(skipPath) > 0 && skipPath[len(skipPath)-1] == '*' {
			prefix := skipPath[:len(skipPath)-1]
			if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
				return true
			}
		}
	}
	return false
}
