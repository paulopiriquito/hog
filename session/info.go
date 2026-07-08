package session

import (
	"encoding/json"
	"net/http"
)

// InfoHandler serves the SPA-facing session view at the session-info endpoint:
// GET ⇒ 200 JSON PublicView (no tokens/fingerprint); no/invalid session ⇒ 401;
// non-GET ⇒ 405. #3 mounts it at the configured infoPath.
func InfoHandler(m Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s, err := m.Read(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.PublicView())
	})
}
