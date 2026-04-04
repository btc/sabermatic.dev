package sample_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/sample"
)

func TestSampleSessionEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/session", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty response body")
	}
}

func TestSampleEvaluationEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/evaluation", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSampleEducatorEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/educator", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSampleCoachEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/coach", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
