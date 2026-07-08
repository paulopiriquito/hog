package terminal

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// cleanRequestPath strips the optional prefix and returns a slash-free fs path
// ("" means the root). It rejects (ok=false) any path whose raw segments include
// a dotfile or traversal segment — anything starting with "." (".env", ".git",
// ".."). The check runs on the raw segments BEFORE path.Clean collapses "..",
// so "/2020..2021" (a "." appears mid-segment, not at the start) is allowed.
// os.Root independently enforces containment as defense in depth.
func cleanRequestPath(urlPath, stripPrefix string) (string, bool) {
	p := urlPath
	if stripPrefix != "" {
		p = strings.TrimPrefix(p, stripPrefix)
	}
	p = strings.TrimPrefix(p, "/")
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			return "", false // dotfile or ".." traversal
		}
	}
	p = path.Clean(p)
	if p == "." {
		return "", true // root
	}
	return p, true
}

// staticConfig is the decoded handler config for `type: static`.
type staticConfig struct {
	Dir          string `yaml:"dir"`
	Index        string `yaml:"index"`
	SPAFallback  *bool  `yaml:"spaFallback"`
	StripPrefix  string `yaml:"stripPrefix"`
	CacheControl string `yaml:"cacheControl"`
}

// registerStatic registers the built-in `static` terminal handler. The factory
// opens the configured dir once (fail-fast) and returns the handler.
func registerStatic(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "static", func(name string, cfg registry.RawConfig) (any, error) {
		var sc staticConfig
		if err := cfg.Decode(&sc); err != nil {
			return nil, fmt.Errorf("static %q: %w", name, err)
		}
		if sc.Dir == "" {
			return nil, fmt.Errorf("static %q: spec.dir is required", name)
		}
		if sc.Index == "" {
			sc.Index = "index.html"
		}
		spaFallback := true
		if sc.SPAFallback != nil {
			spaFallback = *sc.SPAFallback
		}
		root, err := os.OpenRoot(sc.Dir)
		if err != nil {
			return nil, fmt.Errorf("static %q: open dir %q: %w", name, sc.Dir, err)
		}
		return &staticHandler{
			root:         root,
			index:        sc.Index,
			spaFallback:  spaFallback,
			stripPrefix:  sc.StripPrefix,
			cacheControl: sc.CacheControl,
		}, nil
	})
}

// staticHandler serves files from a traversal-safe os.Root with SPA fallback.
type staticHandler struct {
	root         *os.Root
	index        string
	spaFallback  bool
	stripPrefix  string
	cacheControl string
}

// ServeHTTP validates the method, cleans the request path, serves the resolved
// file, and falls back to the SPA shell for extensionless client routes.
func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, ok := cleanRequestPath(r.URL.Path, h.stripPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Resolve the target file name: root and directories map to the index.
	name := rel
	if name == "" {
		name = h.index
	} else if fi, err := h.root.Stat(name); err == nil && fi.IsDir() {
		name = path.Join(name, h.index)
	}
	if h.serveFile(w, r, name) {
		return
	}
	// Miss: SPA fallback for extensionless (client-route) paths.
	if h.spaFallback && path.Ext(rel) == "" {
		if h.serveFile(w, r, h.index) {
			return
		}
	}
	http.NotFound(w, r)
}

// serveFile serves name as a regular file. It returns handled=false when the
// file is missing or the open is rejected (so the caller can fall back); any
// other outcome (served, 5xx, or name is a directory) is terminal for this
// call.
func (h *staticHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) (handled bool) {
	f, err := h.root.Open(name)
	if err != nil {
		// Any open failure — not-exist, permission, or an os.Root containment
		// rejection (e.g. a symlink escaping the root) — is a miss so the caller
		// can fall back. It never escalates to 500, which would leak a status
		// oracle distinguishing an escaping symlink from a plain miss.
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("static: open rejected", "name", name, "err", err)
		}
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		slog.Error("static: stat file", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return true
	}
	if fi.IsDir() {
		return false // a directory is not directly servable; caller falls back
	}
	// Shell (the SPA entry) is always revalidated; other files get the optional
	// configured Cache-Control.
	if path.Base(name) == h.index {
		w.Header().Set("Cache-Control", "no-cache")
	} else if h.cacheControl != "" {
		w.Header().Set("Cache-Control", h.cacheControl)
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, fi.Size(), fi.ModTime().UnixNano()))
	// ServeContent handles MIME, Range, If-None-Match (against our ETag), and
	// If-Modified-Since (against modtime); name is used only for MIME detection.
	http.ServeContent(w, r, name, fi.ModTime(), f)
	return true
}
