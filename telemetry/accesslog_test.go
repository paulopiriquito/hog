package telemetry

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/session"
)

func TestAccessLogEmitsConfiguredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})).With("service", "edge")

	cfg := AccessLogConfig{
		Level:      "info",
		Properties: []string{"method", "status", "user_id", "session_id", "request_id"},
		Headers:    map[string]string{"ua": "User-Agent"},
		Fields:     map[string]string{"env": "prod"},
	}
	mw := AccessLog(cfg, logger)
	// request_id is populated by the request-id middleware on the RESPONSE
	// header (upstream of access-log in the real skeleton); simulate that here
	// since this unit test exercises AccessLog in isolation.
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "rid-1")
		w.WriteHeader(201)
	}))

	req := httptest.NewRequest("GET", "http://h/x", nil)
	req.Header.Set("User-Agent", "UA")
	ctx := session.WithPrincipal(req.Context(), &session.Principal{Subject: "u1", SessionID: "sess-abc"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log not JSON: %v (%s)", err, buf.String())
	}
	for k, want := range map[string]any{
		"service": "edge", "method": "GET", "status": float64(201),
		"user_id": "u1", "session_id": "sess-abc", "request_id": "rid-1",
		"ua": "UA", "env": "prod",
	} {
		if rec[k] != want {
			t.Errorf("field %q = %v, want %v", k, rec[k], want)
		}
	}
	if _, present := rec["path"]; present {
		t.Error("path not configured but present")
	}
}

func TestAccessLogSuppressedByLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mw := AccessLog(AccessLogConfig{Level: "info", Properties: []string{"method"}}, logger)
	mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://h/x", nil))
	if buf.Len() != 0 {
		t.Fatalf("access line emitted despite level: %s", buf.String())
	}
}

func TestAccessRecorderForwardsFlush(t *testing.T) {
	rr := httptest.NewRecorder()
	rc := &accessRecorder{ResponseWriter: rr, status: 200}
	if err := http.NewResponseController(rc).Flush(); err != nil {
		t.Fatalf("Flush through accessRecorder: %v", err)
	}
}

func TestAccessLogRedactsSensitiveQuery(t *testing.T) {
	if got := redactQuery("code=SECRET&page=2&api_key=K"); strings.Contains(got, "SECRET") || strings.Contains(got, "K") {
		t.Fatalf("query not redacted: %q", got)
	}
	if got := redactQuery("code=SECRET&page=2"); !strings.Contains(got, "page=2") {
		t.Fatalf("non-sensitive param dropped: %q", got)
	}
}
