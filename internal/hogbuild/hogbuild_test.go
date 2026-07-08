package hogbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePlugin(t *testing.T) {
	cases := map[string]struct {
		in   string
		path string
		ver  string
		err  bool
	}{
		"versioned":   {"github.com/acme/geo@v1.4.0", "github.com/acme/geo", "v1.4.0", false},
		"unversioned": {"github.com/acme/audit", "github.com/acme/audit", "", false},
		"bad path":    {"not a path!", "", "", true},
		"empty":       {"", "", "", true},
	}
	for name, c := range cases {
		p, err := parsePlugin(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%s: want error", name)
			}
			continue
		}
		if err != nil || p.ImportPath != c.path || p.Version != c.ver {
			t.Errorf("%s: got %+v err=%v", name, p, err)
		}
	}
}

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "g.yaml")
	if err := os.WriteFile(cfg, []byte(`
kind: Gateway
metadata: { name: hog }
spec:
  plugins:
    - github.com/acme/geo@v1.4.0
    - github.com/acme/audit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := ReadManifest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 || plugins[0].ImportPath != "github.com/acme/geo" || plugins[0].Version != "v1.4.0" || plugins[1].Version != "" {
		t.Fatalf("plugins = %+v", plugins)
	}
}

func TestReadManifestRequiresGateway(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "g.yaml")
	_ = os.WriteFile(cfg, []byte("kind: Route\nmetadata: {name: r}\nspec: {match: /x, handler: {type: static, dir: /w}}\n"), 0o600)
	if _, err := ReadManifest(cfg); err == nil {
		t.Fatal("want error when no Gateway resource")
	}
}

func TestValidImportPathRejections(t *testing.T) {
	bad := []string{
		"",                   // empty
		"not a path!",        // whitespace
		"acme/geo",           // no domain (first segment has no dot)
		"github.com/../evil", // traversal segment
		"github.com/x/./y",   // dot segment
		"github.com/x@v1",    // stray @ in path
		"github.com/x\ny",    // newline (injection)
		"github.com/x\"y",    // quote (injection)
		"-x.evil.com/foo",    // leading dash (flag injection)
	}
	for _, p := range bad {
		if validImportPath(p) {
			t.Errorf("validImportPath(%q) = true, want false", p)
		}
	}
	good := []string{"github.com/acme/geo", "gopkg.in/yaml.v3", "example.com/x/y/z"}
	for _, p := range good {
		if !validImportPath(p) {
			t.Errorf("validImportPath(%q) = false, want true", p)
		}
	}
}

func TestReadManifestRejectsDuplicateGateway(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "g.yaml")
	if err := os.WriteFile(cfg, []byte("kind: Gateway\nmetadata: {name: a}\nspec: {}\n---\nkind: Gateway\nmetadata: {name: b}\nspec: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(cfg); err == nil {
		t.Fatal("want error on duplicate Gateway")
	}
}

func TestReadManifestRejectsBadPluginEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "g.yaml")
	if err := os.WriteFile(cfg, []byte("kind: Gateway\nmetadata: {name: hog}\nspec:\n  plugins: [\"acme/no-domain\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(cfg); err == nil {
		t.Fatal("want error on a bad plugin import path in the manifest")
	}
}

func TestRenderMain(t *testing.T) {
	src := RenderMain([]Plugin{{ImportPath: "github.com/acme/geo"}, {ImportPath: "github.com/acme/audit"}})
	for _, want := range []string{
		`package main`,
		`"github.com/paulopiriquito/hog"`,
		`_ "github.com/acme/geo"`,
		`_ "github.com/acme/audit"`,
		`func main() { hog.Main() }`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("main.go missing %q:\n%s", want, src)
		}
	}
	if v := RenderMain(nil); !strings.Contains(v, `"github.com/paulopiriquito/hog"`) || strings.Contains(v, "_ \"") {
		t.Fatalf("vanilla main.go = %s", v)
	}
}

func TestRenderGoMod(t *testing.T) {
	gm := RenderGoMod(Options{
		HogSource: "/src/hog",
		Replaces:  []Replace{{Path: "github.com/acme/geo", With: "./plugins/geo"}},
	})
	for _, want := range []string{
		"module hogbin",
		"go 1.26",
		"replace github.com/paulopiriquito/hog => /src/hog",
		"replace github.com/acme/geo => ./plugins/geo",
	} {
		if !strings.Contains(gm, want) {
			t.Fatalf("go.mod missing %q:\n%s", want, gm)
		}
	}
}

func TestRenderGoModQuotesSpacedPaths(t *testing.T) {
	gm := RenderGoMod(Options{
		HogSource: "/src/hog",
		Replaces:  []Replace{{Path: "github.com/acme/geo", With: "/has space/x"}},
	})
	want := `replace github.com/acme/geo => "/has space/x"`
	if !strings.Contains(gm, want) {
		t.Fatalf("go.mod missing quoted target %q:\n%s", want, gm)
	}
}

func TestModTarget(t *testing.T) {
	cases := map[string]string{
		"/clean/path":   "/clean/path",
		"/has space/x":  `"/has space/x"`,
		"/has\ttab/x":   `"/has\ttab/x"`,
		"/has\"quote/x": `"/has\"quote/x"`,
	}
	for in, want := range cases {
		if got := modTarget(in); got != want {
			t.Errorf("modTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
