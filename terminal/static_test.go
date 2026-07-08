package terminal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"gopkg.in/yaml.v3"
)

func TestCleanRequestPath(t *testing.T) {
	cases := []struct {
		name, urlPath, stripPrefix, want string
		ok                               bool
	}{
		{name: "root", urlPath: "/", want: "", ok: true},
		{name: "file", urlPath: "/assets/app.js", want: "assets/app.js", ok: true},
		{name: "strip prefix", urlPath: "/app/assets/app.js", stripPrefix: "/app", want: "assets/app.js", ok: true},
		{name: "strip prefix root", urlPath: "/app", stripPrefix: "/app", want: "", ok: true},
		{name: "dotfile rejected", urlPath: "/.env", want: "", ok: false},
		{name: "nested dotfile rejected", urlPath: "/foo/.git/config", want: "", ok: false},
		{name: "traversal rejected", urlPath: "/../etc/passwd", want: "", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cleanRequestPath(tc.urlPath, tc.stripPrefix)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("cleanRequestPath(%q,%q) = (%q,%v), want (%q,%v)", tc.urlPath, tc.stripPrefix, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// fixtureDir creates a web root with index.html, assets/app.js, a nested dir
// with its own index.html, and a dotfile. Returns the dir path.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<!doctype html><title>shell</title>")
	mustWrite(t, filepath.Join(dir, "assets", "app.js"), "console.log('hi')")
	mustWrite(t, filepath.Join(dir, "sub", "index.html"), "<!doctype html><title>sub</title>")
	mustWrite(t, filepath.Join(dir, ".env"), "SECRET=1")
	return dir
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildStatic builds the static handler from a YAML spec body.
func buildStatic(t *testing.T, specYAML string) http.Handler {
	t.Helper()
	reg := registry.New()
	Register(reg)
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &node); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	m, err := reg.Build(config.KindTerminalHandler, "static", registry.RawConfig{Node: node})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	h, ok := m.(http.Handler)
	if !ok {
		t.Fatalf("static module is not an http.Handler: %T", m)
	}
	return h
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestStaticServesFile(t *testing.T) {
	dir := fixtureDir(t)
	h := buildStatic(t, "dir: "+dir)
	rec := get(t, h, "GET", "/assets/app.js")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content-type = %q, want javascript", ct)
	}
	if rec.Body.String() != "console.log('hi')" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestStaticFailsFastOnMissingDir(t *testing.T) {
	reg := registry.New()
	Register(reg)
	var node yaml.Node
	_ = yaml.Unmarshal([]byte("dir: /no/such/dir/hog-test"), &node)
	if _, err := reg.Build(config.KindTerminalHandler, "static", registry.RawConfig{Node: node}); err == nil {
		t.Fatal("want error when dir cannot be opened")
	}
}

func TestStaticRootServesIndexNoCache(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "GET", "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "shell") {
		t.Fatalf("body = %q, want the index shell", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestStaticExplicitIndexIsShell(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "GET", "/index.html")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("status=%d cc=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestStaticSubdirServesItsIndex(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "GET", "/sub/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sub") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestStaticSPAFallback(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "GET", "/dashboard") // extensionless client route, no such file
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "shell") {
		t.Fatalf("fallback did not serve the index shell: %q", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("fallback shell must be no-cache, got %q", rec.Header().Get("Cache-Control"))
	}
}

func TestStaticMissingAssetIs404(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	if rec := get(t, h, "GET", "/assets/missing.js"); rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestStaticFallbackDisabled(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t)+"\nspaFallback: false")
	if rec := get(t, h, "GET", "/dashboard"); rec.Code != 404 {
		t.Fatalf("status = %d, want 404 (fallback disabled)", rec.Code)
	}
}

func TestStaticMethodNotAllowed(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "POST", "/")
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if a := rec.Header().Get("Allow"); a != "GET, HEAD" {
		t.Fatalf("Allow = %q, want \"GET, HEAD\"", a)
	}
}

func TestStaticDotfileBlocked(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	if rec := get(t, h, "GET", "/.env"); rec.Code != 404 {
		t.Fatalf("status = %d, want 404 for dotfile", rec.Code)
	}
}

func TestStaticTraversalContained(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	// Normalizes to /etc/passwd inside the root, which does not exist → 404.
	// (Confirms os.Root containment: it never escapes the web root.)
	if rec := get(t, h, "GET", "/../../../../etc/passwd"); rec.Code != 404 {
		t.Fatalf("status = %d, want 404 (contained)", rec.Code)
	}
}

func TestStaticConditional304(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	first := get(t, h, "GET", "/assets/app.js")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
}

func TestStaticRangeRequest(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Header.Set("Range", "bytes=0-3")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.String() != "cons" { // first 4 bytes of "console.log('hi')"
		t.Fatalf("range body = %q, want \"cons\"", rec.Body.String())
	}
}

func TestStaticCacheControlOnAssetNotShell(t *testing.T) {
	dir := fixtureDir(t)
	h := buildStatic(t, "dir: "+dir+"\ncacheControl: \"public, max-age=31536000\"")
	asset := get(t, h, "GET", "/assets/app.js")
	if cc := asset.Header().Get("Cache-Control"); cc != "public, max-age=31536000" {
		t.Fatalf("asset Cache-Control = %q", cc)
	}
	shell := get(t, h, "GET", "/")
	if cc := shell.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("shell Cache-Control = %q, want no-cache (overrides cacheControl)", cc)
	}
}

func TestStaticStripPrefix(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t)+"\nstripPrefix: /app")
	rec := get(t, h, "GET", "/app/assets/app.js")
	if rec.Code != 200 || rec.Body.String() != "console.log('hi')" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestStaticHEAD(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	rec := get(t, h, "HEAD", "/assets/app.js")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD body should be empty, got %q", rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("HEAD should still set ETag")
	}
}

func TestStaticEncodedTraversalContained(t *testing.T) {
	h := buildStatic(t, "dir: "+fixtureDir(t))
	// %2f decodes to "/", so this normalizes to ../../etc/passwd and is rejected.
	if rec := get(t, h, "GET", "/..%2f..%2fetc/passwd"); rec.Code != 404 {
		t.Fatalf("status = %d, want 404 (encoded traversal contained)", rec.Code)
	}
}
