// Command backend is the e2e stack's stand-in upstream service. It echoes
// enough of the inbound request back as JSON that the e2e tests can assert on
// what HOG forwarded (identity headers, bearer token, and whether the Cookie
// header was stripped).
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type echoResponse struct {
	Path       string `json:"path"`
	UserID     string `json:"user_id"`
	UserGroups string `json:"user_groups"`
	HasBearer  bool   `json:"has_bearer"`
	Cookie     string `json:"cookie"`
}

func echo(w http.ResponseWriter, r *http.Request) {
	resp := echoResponse{
		Path:       r.URL.Path,
		UserID:     r.Header.Get("X-User-Id"),
		UserGroups: r.Header.Get("X-User-Groups"),
		HasBearer:  r.Header.Get("Authorization") != "",
		Cookie:     r.Header.Get("Cookie"),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func value(v int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"v": v})
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", echo)
	mux.HandleFunc("/one", value(1))
	mux.HandleFunc("/two", value(2))
	log.Print("backend listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", mux))
}
