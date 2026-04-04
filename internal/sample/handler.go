package sample

import (
	"net/http"
)

// RegisterRoutes adds the public sample data endpoints to the mux.
// These require no authentication.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sample/session", serveFixture("fixtures/session.json"))
	mux.HandleFunc("GET /api/sample/evaluation", serveFixture("fixtures/evaluation.json"))
	mux.HandleFunc("GET /api/sample/educator", serveFixture("fixtures/educator.json"))
	mux.HandleFunc("GET /api/sample/coach", serveFixture("fixtures/coach.json"))
}

func serveFixture(path string) http.HandlerFunc {
	// Read at init time — embedded files don't change
	data, err := fixtureFS.ReadFile(path)
	if err != nil {
		panic("missing fixture: " + path)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data) //nolint:errcheck
	}
}
