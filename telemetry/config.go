// Package telemetry wires OpenTelemetry (OTLP traces + metrics), W3C trace
// propagation, and a configurable, trace-correlated slog access log into HOG.
package telemetry

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/config"
)

// Config is the decoded, validated `kind: Telemetry` spec.
type Config struct {
	LogLevel  string          `yaml:"logLevel"`
	OTLP      OTLPConfig      `yaml:"otlp"`
	Service   ServiceConfig   `yaml:"service"`
	Sampling  SamplingConfig  `yaml:"sampling"`
	AccessLog AccessLogConfig `yaml:"accessLog"`
}

// OTLPConfig configures the OTLP exporter (traces + metrics).
type OTLPConfig struct {
	Endpoint string            `yaml:"endpoint"`
	Protocol string            `yaml:"protocol"`
	Headers  map[string]string `yaml:"headers"`
	Insecure bool              `yaml:"insecure"`
	Timeout  string            `yaml:"timeout"`
}

// ServiceConfig sets the resource service.name/version.
type ServiceConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// SamplingConfig is the head parent-based sampling ratio.
type SamplingConfig struct {
	Ratio float64 `yaml:"ratio"`
}

// AccessLogConfig is the access-log field configuration.
type AccessLogConfig struct {
	Level      string            `yaml:"level"`
	Properties []string          `yaml:"properties"`
	Headers    map[string]string `yaml:"headers"` // field name -> request header
	Fields     map[string]string `yaml:"fields"`  // static custom fields (${ENV} resolved at load)
}

const (
	protoHTTP = "http/protobuf"
	protoGRPC = "grpc"
)

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
}

// allowedProperties is the fixed access-log property allowlist.
var allowedProperties = map[string]bool{
	"method": true, "path": true, "route": true, "query": true, "status": true,
	"duration_ms": true, "bytes_out": true, "client_ip": true, "host": true,
	"protocol": true, "user_id": true, "session_id": true, "request_id": true,
	"trace_id": true, "span_id": true,
}

// Default is the config used when no kind: Telemetry resource is present.
func Default() Config {
	return Config{
		LogLevel: "info",
		OTLP:     OTLPConfig{Protocol: protoHTTP},
		Service:  ServiceConfig{Name: "hog"},
		Sampling: SamplingConfig{Ratio: 1.0},
		AccessLog: AccessLogConfig{
			Level:      "info",
			Properties: []string{"method", "path", "status", "duration_ms", "client_ip", "request_id", "trace_id", "span_id"},
		},
	}
}

// FromResource decodes and validates a kind: Telemetry resource. service.name is
// required; all other fields default. Fails fast on any invalid value.
func FromResource(r config.Resource) (Config, error) {
	c := Default()
	c.Service.Name = ""          // required: must come from the document, not the default
	c.AccessLog.Properties = nil // an explicit block replaces the default set
	if err := r.Spec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("telemetry %q: %w", r.Metadata.Name, err)
	}
	if c.Service.Name == "" {
		return Config{}, fmt.Errorf("telemetry %q: service.name is required", r.Metadata.Name)
	}
	if _, ok := logLevels[c.LogLevel]; !ok {
		return Config{}, fmt.Errorf("telemetry %q: logLevel %q (want debug|info|warn|error)", r.Metadata.Name, c.LogLevel)
	}
	if c.OTLP.Protocol != protoHTTP && c.OTLP.Protocol != protoGRPC {
		return Config{}, fmt.Errorf("telemetry %q: otlp.protocol %q (want %s|%s)", r.Metadata.Name, c.OTLP.Protocol, protoHTTP, protoGRPC)
	}
	if c.OTLP.Timeout != "" {
		if _, err := time.ParseDuration(c.OTLP.Timeout); err != nil {
			return Config{}, fmt.Errorf("telemetry %q: otlp.timeout: %w", r.Metadata.Name, err)
		}
	}
	if c.OTLP.Endpoint != "" {
		u, uerr := url.Parse(c.OTLP.Endpoint)
		if uerr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Config{}, fmt.Errorf("telemetry %q: otlp.endpoint %q must be an http(s) URL like http://collector:4318", r.Metadata.Name, c.OTLP.Endpoint)
		}
	}
	if c.Sampling.Ratio < 0 || c.Sampling.Ratio > 1 {
		return Config{}, fmt.Errorf("telemetry %q: sampling.ratio %v (want 0..1)", r.Metadata.Name, c.Sampling.Ratio)
	}
	if _, ok := logLevels[c.AccessLog.Level]; !ok {
		return Config{}, fmt.Errorf("telemetry %q: accessLog.level %q (want debug|info|warn|error)", r.Metadata.Name, c.AccessLog.Level)
	}
	if c.AccessLog.Properties == nil {
		c.AccessLog.Properties = Default().AccessLog.Properties
	}
	for _, p := range c.AccessLog.Properties {
		if !allowedProperties[p] {
			return Config{}, fmt.Errorf("telemetry %q: unknown accessLog property %q", r.Metadata.Name, p)
		}
	}
	denied := map[string]bool{"authorization": true, "cookie": true, "proxy-authorization": true, "set-cookie": true}
	for _, h := range c.AccessLog.Headers {
		if denied[strings.ToLower(h)] {
			return Config{}, fmt.Errorf("telemetry %q: accessLog header %q is a credential and cannot be captured", r.Metadata.Name, h)
		}
	}
	return c, nil
}

// SlogLevel returns the parsed slog level for the app logger (logLevel).
func (c Config) SlogLevel() slog.Level { return logLevels[c.LogLevel] }

// SlogLevel returns the parsed slog level access lines are emitted at.
func (c AccessLogConfig) SlogLevel() slog.Level { return logLevels[c.Level] }
