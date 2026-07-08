// Package plugin is a test fixture: its init() registers a `testecho` terminal.
package plugin

import (
	"net/http"

	"github.com/paulopiriquito/hog"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

func init() {
	hog.Register(config.KindTerminalHandler, "testecho", func(string, registry.RawConfig) (any, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("plugin-ok"))
		}), nil
	})
}
