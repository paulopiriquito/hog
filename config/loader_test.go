package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDirSortedAndExpanded(t *testing.T) {
	dir := t.TempDir()
	// Filenames chosen so sorted order is a.yaml then b.yaml.
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"),
		[]byte("kind: Route\nmetadata: {name: two}\nspec: {match: /two}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"),
		[]byte("kind: Gateway\nmetadata: {name: hog}\nspec: {listen: \"${PORT:-:8080}\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("got %d resources", len(rs))
	}
	if rs[0].Kind != "Gateway" || rs[1].Metadata.Name != "two" {
		t.Fatalf("document order wrong: %q, %q", rs[0].Kind, rs[1].Metadata.Name)
	}
	var listen string
	if err := rs[0].Spec.Decode(&struct {
		Listen *string `yaml:"listen"`
	}{Listen: &listen}); err != nil {
		t.Fatal(err)
	}
	if listen != ":8080" {
		t.Fatalf("listen = %q, want :8080 (default applied)", listen)
	}
}

func TestLoadSingleFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hog.yaml")
	os.WriteFile(p, []byte("kind: Gateway\nmetadata: {name: hog}\nspec: {}\n"), 0o600)
	rs, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d", len(rs))
	}
}
