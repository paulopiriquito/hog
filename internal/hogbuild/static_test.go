package hogbuild

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/paulopiriquito/hog/app"
	"github.com/paulopiriquito/hog/config"
)

// TestBakedStaticConfigParses guards the baked hog-static config (embedded in
// the runtime image as /etc/hog/gateway.yaml) against drift: it must always
// load and parse into a single public static route. No Docker needed.
func TestBakedStaticConfigParses(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	cfgPath := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "build", "static", "gateway.yaml"))
	resources, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load baked config: %v", err)
	}
	cfg, err := app.Parse(resources)
	if err != nil {
		t.Fatalf("parse baked config: %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Handler.Type != "static" {
		t.Fatalf("baked SPA config: want one static route, got %+v", cfg.Routes)
	}
}
