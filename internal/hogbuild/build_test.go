package hogbuild

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0) // .../internal/hogbuild/build_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestBuildComposesPluginBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: runs the go toolchain")
	}
	root := repoRoot(t)
	pluginDir := filepath.Join(root, "internal", "hogbuild", "testdata", "plugin")

	work := t.TempDir()
	addr := freeAddr(t)
	cfg := filepath.Join(work, "gateway.yaml")
	if err := os.WriteFile(cfg, []byte(`
kind: Gateway
metadata: { name: hog }
spec:
  listen: "`+addr+`"
  plugins:
    - example.com/hogtestplugin
---
kind: Route
metadata: { name: p }
spec: { match: /p, handler: { type: testecho }, access: { auth: public } }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(work, "hog")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := Build(ctx, cfg, Options{
		Output:    bin,
		HogSource: root,
		Replaces:  []Replace{{Path: "example.com/hogtestplugin", With: pluginDir}},
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}

	cmd := exec.CommandContext(ctx, bin, "--config", cfg)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	var body string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/p")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.TrimSpace(body) != "plugin-ok" {
		t.Fatalf("plugin route body = %q, want plugin-ok (plugin not compiled in?)", body)
	}
}

func TestHasControl(t *testing.T) {
	bad := []string{"a\nb", "a\rb", "a\x00b", "\x1b"} // newline, CR, NUL, ESC
	for _, s := range bad {
		if !hasControl(s) {
			t.Errorf("hasControl(%q) = false, want true", s)
		}
	}
	ok := []string{"/abs/path", "github.com/acme/geo", "a\tb", "with space", ""} // tab + space allowed (can't break a go.mod line)
	for _, s := range ok {
		if hasControl(s) {
			t.Errorf("hasControl(%q) = true, want false", s)
		}
	}
}

func TestAbsReplaces(t *testing.T) {
	out, err := absReplaces([]Replace{{Path: "example.com/x", With: "./plugins/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(out[0].With) {
		t.Fatalf("relative --replace target not absolutized: %q", out[0].With)
	}
	if out[0].Path != "example.com/x" {
		t.Fatalf("Path must be unchanged, got %q", out[0].Path)
	}
}

func TestModuleReplaced(t *testing.T) {
	replaces := []Replace{{Path: "example.com/x", With: "./x"}}
	cases := map[string]bool{
		"example.com/x":     true,  // exact
		"example.com/x/sub": true,  // under the module
		"example.com/xyz":   false, // sibling prefix — must NOT match
		"example.com/y":     false, // unrelated
		"example.com/xy/z":  false, // sibling prefix with more path
	}
	for path, want := range cases {
		if got := moduleReplaced(path, replaces); got != want {
			t.Errorf("moduleReplaced(%q) = %v, want %v", path, got, want)
		}
	}
	if moduleReplaced("anything", nil) {
		t.Error("moduleReplaced with no replaces must be false")
	}
}
