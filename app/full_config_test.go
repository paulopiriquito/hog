package app

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/paulopiriquito/hog/config"
)

func TestFullConfigExampleParses(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "website", "docs", "examples", "full-config.yaml"))
	rs, err := config.Load(p)
	if err != nil {
		t.Fatalf("load full-config example: %v", err)
	}
	if _, err := Parse(rs); err != nil {
		t.Fatalf("parse full-config example: %v", err)
	}
}
