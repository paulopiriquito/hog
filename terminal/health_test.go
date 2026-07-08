package terminal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paulopiriquito/hog/registry"
)

func TestHealthHandler(t *testing.T) {
	reg := registry.New()
	Register(reg) // registers the built-in terminal handlers on reg

	h, err := reg.Build("TerminalHandler", "health", registry.RawConfig{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	handler, ok := h.(http.Handler)
	if !ok {
		t.Fatalf("health module is not an http.Handler: %T", h)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
