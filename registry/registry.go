// Package registry is the compile-time module registry. Modules self-register
// at init() under a kind+name; the app builds instances by name from config.
package registry

import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

// RawConfig wraps an undecoded spec node so a Factory can decode it into its
// own typed struct.
type RawConfig struct{ Node yaml.Node }

// Decode unmarshals the raw spec into v. A zero RawConfig decodes to nothing.
func (c RawConfig) Decode(v any) error {
	if c.Node.Kind == 0 {
		return nil
	}
	return c.Node.Decode(v)
}

// Factory builds a configured module instance. instanceName is the resource's
// metadata.name, useful for error messages and logging.
type Factory func(instanceName string, cfg RawConfig) (any, error)

// Registry maps (kind, name) to a Factory.
type Registry struct {
	mu     sync.RWMutex
	byKind map[string]map[string]Factory
}

// New returns an empty registry.
func New() *Registry { return &Registry{byKind: map[string]map[string]Factory{}} }

// Register adds a factory. It panics on duplicate (kind, name) — a programming
// error, surfaced at startup.
func (r *Registry) Register(kind, name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKind[kind] == nil {
		r.byKind[kind] = map[string]Factory{}
	}
	if _, dup := r.byKind[kind][name]; dup {
		panic(fmt.Sprintf("registry: duplicate module %s/%s", kind, name))
	}
	r.byKind[kind][name] = f
}

// Build instantiates the named module of the given kind.
func (r *Registry) Build(kind, name string, cfg RawConfig) (any, error) {
	r.mu.RLock()
	f, ok := r.byKind[kind][name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("registry: no module %s/%s", kind, name)
	}
	return f(name, cfg)
}

// Default is the process-wide registry used by the framework API.
var Default = New()

// Register registers a module on the Default registry (called from init()).
func Register(kind, name string, f Factory) { Default.Register(kind, name, f) }
