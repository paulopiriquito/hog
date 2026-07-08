package session

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paulopiriquito/hog/idp"
)

func TestInfoHandler(t *testing.T) {
	m := testManager(t, nil)
	h := InfoHandler(m)

	// authed: build+write a session, then GET with those cookies
	idt := &idp.Identity{Subject: "u-1"}
	tok := &idp.Tokens{AccessToken: "at-secret", Expiry: time.Now().Add(time.Hour)}
	wr := httptest.NewRecorder()
	rq := login(httptest.NewRequest("GET", "/auth/session", nil))
	_ = m.Write(wr, rq, m.New(idt, map[string]any{"email": "a@b.co"}, tok, rq))

	rq2 := login(httptest.NewRequest("GET", "/auth/session", nil))
	for _, c := range wr.Result().Cookies() {
		rq2.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, rq2)
	if rec.Code != 200 {
		t.Fatalf("authed status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if containsStr(body, "at-secret") {
		t.Fatalf("info body leaks access token: %s", body)
	}
	var pv PublicView
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil || pv.Subject != "u-1" {
		t.Fatalf("decode pv: %v %+v", err, pv)
	}

	// unauthed ⇒ 401
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/auth/session", nil))
	if rec2.Code != 401 {
		t.Fatalf("unauthed status = %d, want 401", rec2.Code)
	}

	// non-GET ⇒ 405
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest("POST", "/auth/session", nil))
	if rec3.Code != 405 {
		t.Fatalf("POST status = %d, want 405", rec3.Code)
	}
}
