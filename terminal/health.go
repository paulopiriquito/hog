// Package terminal provides built-in terminal (route-final) handlers. The core
// spine ships only `health`; static/proxy/api/auth/system handlers are added by
// later specs.
package terminal

import (
	"io"
	"net/http"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// Register adds the built-in terminal handlers to reg.
func Register(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "health", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"status":"ok"}`)
		}), nil
	})
}
