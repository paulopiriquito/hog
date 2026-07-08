package registry

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRegisterAndBuild(t *testing.T) {
	r := New()
	type widget struct{ Size int }
	r.Register("ResponsePlugin", "widget", func(name string, cfg RawConfig) (any, error) {
		var spec struct {
			Size int `yaml:"size"`
		}
		if err := cfg.Decode(&spec); err != nil {
			return nil, err
		}
		return &widget{Size: spec.Size}, nil
	})

	var node yaml.Node
	if err := yaml.Unmarshal([]byte("size: 7"), &node); err != nil {
		t.Fatal(err)
	}
	got, err := r.Build("ResponsePlugin", "widget", RawConfig{Node: node})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.(*widget).Size != 7 {
		t.Fatalf("size = %d, want 7", got.(*widget).Size)
	}
}

func TestBuildUnknown(t *testing.T) {
	r := New()
	if _, err := r.Build("ResponsePlugin", "nope", RawConfig{}); err == nil {
		t.Fatal("want error for unknown module")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	r := New()
	f := func(string, RawConfig) (any, error) { return nil, nil }
	r.Register("K", "n", f)
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on duplicate registration")
		}
	}()
	r.Register("K", "n", f)
}
