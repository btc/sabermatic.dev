package handler

import (
	"net/http"
	"testing"
)

func TestIsCSRFExempt(t *testing.T) {
	// Use literal prefixes to avoid importing rpc (heavy dependency chain).
	prefixes := []string{"drill.v1.SampleService", "drill.v1.AuthService"}

	tests := []struct {
		name   string
		path   string
		method string
		want   bool
	}{
		{"stripe webhook POST", "/api/webhooks/stripe", http.MethodPost, true},
		{"stripe webhook GET", "/api/webhooks/stripe", http.MethodGet, false},
		{"riverui root", "/admin/jobs", http.MethodGet, true},
		{"riverui subpath", "/admin/jobs/queues", http.MethodGet, true},
		{"connect service", "/drill.v1.SampleService/GetSampleSession", http.MethodPost, true},
		{"connect auth", "/drill.v1.AuthService/Login", http.MethodPost, true},
		{"health endpoint", "/api/health", http.MethodGet, false},
		{"spa root", "/", http.MethodGet, false},
		{"random POST", "/api/foo", http.MethodPost, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCSRFExempt(tt.path, tt.method, prefixes)
			if got != tt.want {
				t.Errorf("isCSRFExempt(%q, %q) = %v, want %v", tt.path, tt.method, got, tt.want)
			}
		})
	}
}
