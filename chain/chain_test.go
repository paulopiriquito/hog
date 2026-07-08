package chain

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recorder middleware appends a marker on the way in.
func mark(order *[]string, name string) Middleware {
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name)
			next.ServeHTTP(w, r)
		})
	})
}

func TestComposeOrder(t *testing.T) {
	var order []string
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "terminal")
		w.WriteHeader(200)
	})
	h := Compose(terminal, mark(&order, "a"), mark(&order, "b"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	got := strings.Join(order, ",")
	if got != "a,b,terminal" {
		t.Fatalf("order = %q, want a,b,terminal", got)
	}
}

func TestBufferingWriterRewrite(t *testing.T) {
	// A response-shaping middleware: buffer the terminal output, uppercase it.
	shaper := Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := NewBuffer(w)
			next.ServeHTTP(buf, r)
			body := strings.ToUpper(string(buf.Body()))
			w.WriteHeader(buf.Status())
			io.WriteString(w, body)
		})
	})
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		io.WriteString(w, "hello")
	})
	rec := httptest.NewRecorder()
	Compose(terminal, shaper).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if rec.Body.String() != "HELLO" {
		t.Fatalf("body = %q, want HELLO", rec.Body.String())
	}
}

func TestSkeletonNamesAndOrder(t *testing.T) {
	got := SkeletonNames()
	want := []string{
		"recover", "request-id", "access-log",
		"security", "session", "auth-gate", "authz", "projection",
	}
	if len(got) != len(want) {
		t.Fatalf("skeleton len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("skeleton[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := Compose(boom, Skeleton(nil, Gates{})...)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRequestIDHeaderSet(t *testing.T) {
	var seen string
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = w.Header().Get("X-Request-Id")
	})
	h := Compose(terminal, Skeleton(nil, Gates{})...)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen == "" {
		t.Fatal("X-Request-Id not set by skeleton")
	}
}

func TestRequestIDPassthrough(t *testing.T) {
	var seen string
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = w.Header().Get("X-Request-Id")
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", "my-trace-id")
	Compose(terminal, requestIDMW()).ServeHTTP(httptest.NewRecorder(), req)
	if seen != "my-trace-id" {
		t.Fatalf("passthrough = %q, want my-trace-id", seen)
	}
}

func TestSkeletonInjectsGates(t *testing.T) {
	var ran []string
	mk := func(name string) Middleware {
		return Func(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ran = append(ran, name)
				next.ServeHTTP(w, r)
			})
		})
	}
	gates := Gates{Session: mk("session"), AuthGate: mk("auth"), Projection: mk("proj")}
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = append(ran, "terminal") })
	Compose(terminal, Skeleton(nil, gates)...).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	// The injected gates run in their fixed skeleton positions, before the terminal;
	// the un-instrumented reserved slots (recover/request-id/access-log/security/authz)
	// are pass-throughs and contribute nothing to ran.
	got := strings.Join(ran, ",")
	if got != "session,auth,proj,terminal" {
		t.Fatalf("gate order = %q, want \"session,auth,proj,terminal\"", got)
	}
	// Cross-check the gates occupy the named slots in the fixed order.
	names := SkeletonNames()
	if indexOf(names, "session") >= indexOf(names, "auth-gate") ||
		indexOf(names, "auth-gate") >= indexOf(names, "authz") ||
		indexOf(names, "authz") >= indexOf(names, "projection") {
		t.Fatalf("skeleton slot order wrong: %v", names)
	}
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

func TestSkeletonNilGatesAllReserved(t *testing.T) {
	// Skeleton(nil, Gates{}) must behave exactly like the old all-reserved skeleton.
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	rec := httptest.NewRecorder()
	Compose(terminal, Skeleton(nil, Gates{})...).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSkeletonPartialGatesLeavesOthersReserved(t *testing.T) {
	var ran []string
	sessionMW := Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ran = append(ran, "session")
			next.ServeHTTP(w, r)
		})
	})
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = append(ran, "terminal") })
	Compose(terminal, Skeleton(nil, Gates{Session: sessionMW})...).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(ran, ",") != "session,terminal" {
		t.Fatalf("partial gates ran = %v, want [session terminal]", ran)
	}
}
