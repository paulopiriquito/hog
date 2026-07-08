package telemetry

import (
	"testing"

	"github.com/paulopiriquito/hog/config"
)

func cfgResource(t *testing.T, spec string) config.Resource {
	t.Helper()
	rs, err := config.DecodeAll([]byte("kind: Telemetry\nmetadata: { name: telemetry }\nspec:\n" + spec))
	if err != nil || len(rs) != 1 {
		t.Fatalf("decode: %v (n=%d)", err, len(rs))
	}
	return rs[0]
}

func TestConfigDefaults(t *testing.T) {
	c := Default()
	if c.Service.Name != "hog" {
		t.Fatalf("default service = %q, want hog", c.Service.Name)
	}
	if c.LogLevel != "info" {
		t.Fatalf("default logLevel = %q", c.LogLevel)
	}
	if c.AccessLog.Level != "info" {
		t.Fatalf("default access level = %q", c.AccessLog.Level)
	}
	want := []string{"method", "path", "status", "duration_ms", "client_ip", "request_id", "trace_id", "span_id"}
	if len(c.AccessLog.Properties) != len(want) {
		t.Fatalf("default properties = %v", c.AccessLog.Properties)
	}
}

func TestConfigParsesAndValidates(t *testing.T) {
	c, err := FromResource(cfgResource(t, `
  logLevel: warn
  service: { name: gw, version: 1.2.3 }
  otlp: { endpoint: http://collector:4318, protocol: http/protobuf, timeout: 5s }
  sampling: { ratio: 0.5 }
  accessLog:
    level: info
    properties: [method, status, session_id, user_id]
    headers: { ua: User-Agent }
    fields: { env: prod }
`))
	if err != nil {
		t.Fatalf("FromResource: %v", err)
	}
	if c.Service.Name != "gw" || c.OTLP.Endpoint != "http://collector:4318" || c.Sampling.Ratio != 0.5 {
		t.Fatalf("parsed = %+v", c)
	}
	if c.AccessLog.Headers["ua"] != "User-Agent" || c.AccessLog.Fields["env"] != "prod" {
		t.Fatalf("accesslog maps = %+v", c.AccessLog)
	}
	if c.LogLevel != "warn" || c.OTLP.Protocol != "http/protobuf" || c.OTLP.Timeout != "5s" {
		t.Fatalf("parsed = %+v", c)
	}
	if c.Service.Version != "1.2.3" || c.AccessLog.Level != "info" || len(c.AccessLog.Properties) != 4 {
		t.Fatalf("parsed = %+v", c)
	}
}

func TestConfigErrors(t *testing.T) {
	cases := map[string]string{
		"missing service.name": "  otlp: { endpoint: x }\n",
		"bad logLevel":         "  service: { name: a }\n  logLevel: loud\n",
		"bad protocol":         "  service: { name: a }\n  otlp: { endpoint: x, protocol: carrier-pigeon }\n",
		"bad ratio":            "  service: { name: a }\n  sampling: { ratio: 2 }\n",
		"bad access level":     "  service: { name: a }\n  accessLog: { level: screaming }\n",
		"unknown property":     "  service: { name: a }\n  accessLog: { properties: [method, wat] }\n",
		"bad timeout":          "  service: { name: a }\n  otlp: { endpoint: x, timeout: soon }\n",
		"empty access level":   "  service: { name: a }\n  accessLog: { level: \"\" }\n",
		"empty protocol":       "  service: { name: a }\n  otlp: { endpoint: x, protocol: \"\" }\n",
		"scheme-less endpoint": "  service: { name: a }\n  otlp: { endpoint: \"collector:4317\" }\n",
		"malformed endpoint":   "  service: { name: a }\n  otlp: { endpoint: \"://nope\" }\n",
		"credential header":    "  service: { name: a }\n  accessLog: { headers: { c: Cookie } }\n",
	}
	for name, spec := range cases {
		if _, err := FromResource(cfgResource(t, spec)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestConfigAcceptsZeroSamplingRatio(t *testing.T) {
	c, err := FromResource(cfgResource(t, "  service: { name: a }\n  sampling: { ratio: 0 }\n"))
	if err != nil {
		t.Fatalf("ratio 0 should be valid: %v", err)
	}
	if c.Sampling.Ratio != 0 {
		t.Fatalf("ratio = %v", c.Sampling.Ratio)
	}
}

func TestConfigServiceOnlyDefaults(t *testing.T) {
	c, err := FromResource(cfgResource(t, "  service: { name: only }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" || c.OTLP.Protocol != "http/protobuf" || c.AccessLog.Level != "info" {
		t.Fatalf("defaults not applied through FromResource: %+v", c)
	}
	if len(c.AccessLog.Properties) != len(Default().AccessLog.Properties) {
		t.Fatalf("default properties not applied: %v", c.AccessLog.Properties)
	}
}
