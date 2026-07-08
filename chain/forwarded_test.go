package chain

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	if _, err := ParseTrustedProxies([]string{"bogus"}); err == nil {
		t.Fatal("want error for a non-IP/CIDR entry")
	}
	tp, err := ParseTrustedProxies([]string{"10.0.0.0/8", "192.168.1.5", "*"})
	if err != nil {
		t.Fatal(err)
	}
	if !tp.all {
		t.Fatal(`"*" should set trust-all`)
	}
}

func TestForwardedStripsUntrusted(t *testing.T) {
	tp, _ := ParseTrustedProxies(nil) // empty ⇒ trust nothing
	var seenProto, seenFor, seenRealIP, seenPort, seenHost, seenForwarded string
	h := Forwarded(tp).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenProto = r.Header.Get("X-Forwarded-Proto")
		seenFor = r.Header.Get("X-Forwarded-For")
		seenRealIP = r.Header.Get("X-Real-IP")
		seenPort = r.Header.Get("X-Forwarded-Port")
		seenHost = r.Header.Get("X-Forwarded-Host")
		seenForwarded = r.Header.Get("Forwarded")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:5555" // not trusted
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "9.9.9.9")
	req.Header.Set("X-Forwarded-Port", "12345")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("Forwarded", "for=1.2.3.4;host=evil.example;proto=http")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seenProto != "" || seenFor != "" || seenRealIP != "" || seenPort != "" || seenHost != "" || seenForwarded != "" {
		t.Fatalf("untrusted X-Forwarded-*/X-Real-IP/Forwarded must be stripped, got proto=%q for=%q real-ip=%q port=%q host=%q forwarded=%q",
			seenProto, seenFor, seenRealIP, seenPort, seenHost, seenForwarded)
	}
}

func TestForwardedPreservesTrusted(t *testing.T) {
	tp, _ := ParseTrustedProxies([]string{"203.0.113.0/24"})
	var seen string
	h := Forwarded(tp).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Forwarded-Proto")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:5555" // trusted (in /24)
	req.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "https" {
		t.Fatalf("trusted X-Forwarded-Proto must be preserved, got %q", seen)
	}
}
