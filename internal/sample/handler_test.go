package sample_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/sample"
)

func TestSampleEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	endpoints := []string{
		"/api/sample/session",
		"/api/sample/evaluation",
		"/api/sample/educator",
		"/api/sample/coach",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest("GET", ep, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("expected application/json, got %s", ct)
			}
			if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
				t.Fatalf("expected Cache-Control public, max-age=3600, got %s", cc)
			}
			if w.Body.Len() == 0 {
				t.Fatal("empty response body")
			}
		})
	}
}
