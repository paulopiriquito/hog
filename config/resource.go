package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Metadata is the common identity block of every resource.
type Metadata struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

// Resource is one Kubernetes-style config document. Spec is left as a raw
// node so each kind can decode it into its own typed struct later.
type Resource struct {
	APIVersion string    `yaml:"apiVersion"`
	Kind       string    `yaml:"kind"`
	Metadata   Metadata  `yaml:"metadata"`
	Spec       yaml.Node `yaml:"spec"`
}

// Kind constants are the YAML `kind:` values and the extension-point kinds used
// as registry keys. Centralized so resource parsing and module registration
// cannot drift out of sync.
const (
	KindGateway         = "Gateway"
	KindRoute           = "Route"
	KindRouteGroup      = "RouteGroup"
	KindRequestPlugin   = "RequestPlugin"
	KindResponsePlugin  = "ResponsePlugin"
	KindTerminalHandler = "TerminalHandler"
	KindIdP             = "IdP"
	KindStateProvider   = "StateProvider"
	KindTelemetry       = "Telemetry"
)

// DecodeAll decodes a multi-document YAML stream into resources, skipping
// empty documents.
func DecodeAll(data []byte) ([]Resource, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []Resource
	for {
		var r Resource
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode resource: %w", err)
		}
		if r.Kind == "" { // empty / blank document
			continue
		}
		out = append(out, r)
	}
}
