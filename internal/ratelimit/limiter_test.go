package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/config"
)

func TestClientIP_XForwardedFor(t *testing.T) {
	tests := []struct {
		name   string
		xff    string
		wantIP string
	}{
		{
			name:   "single IP",
			xff:    "1.2.3.4",
			wantIP: "1.2.3.4",
		},
		{
			name:   "multiple IPs returns leftmost",
			xff:    "1.2.3.4, 5.6.7.8, 9.10.11.12",
			wantIP: "1.2.3.4",
		},
		{
			name:   "with spaces",
			xff:    "  1.2.3.4 , 5.6.7.8",
			wantIP: "1.2.3.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Forwarded-For", tt.xff)
			r.RemoteAddr = "99.99.99.99:12345"

			got := ClientIP(r)
			if got != tt.wantIP {
				t.Errorf("ClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestClientIP_FallbackRemoteAddr(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		wantIP     string
	}{
		{
			name:       "IP:port",
			remoteAddr: "1.2.3.4:56789",
			wantIP:     "1.2.3.4",
		},
		{
			name:       "bare IP (no port)",
			remoteAddr: "1.2.3.4",
			wantIP:     "1.2.3.4",
		},
		{
			name:       "IPv6 with port",
			remoteAddr: "[::1]:8080",
			wantIP:     "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Del("X-Forwarded-For")
			r.RemoteAddr = tt.remoteAddr

			got := ClientIP(r)
			if got != tt.wantIP {
				t.Errorf("ClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   10,
		AuthBurst:  5,
		MaxEntries: 100,
	})

	for i := 0; i < 5; i++ {
		if !lim.Allow("testkey") {
			t.Fatalf("Allow() returned false on request %d, expected true (under burst limit)", i+1)
		}
	}
}

func TestAllow_OverLimit(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   0.1, // very slow refill
		AuthBurst:  3,
		MaxEntries: 100,
	})

	// Consume burst
	for i := 0; i < 3; i++ {
		if !lim.Allow("testkey") {
			t.Fatalf("Allow() returned false on request %d, expected true (within burst)", i+1)
		}
	}

	// Next request should be rejected
	if lim.Allow("testkey") {
		t.Fatal("Allow() returned true after burst exhausted, expected false")
	}
}

func TestAllow_EvictsLRU(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   100,
		AuthBurst:  100,
		MaxEntries: 2,
	})

	// Add two entries
	lim.Allow("key1")
	lim.Allow("key2")

	// Adding a third should evict the LRU entry (key1, since key2 was accessed more recently)
	lim.Allow("key3")

	if lim.cache.Contains("key1") {
		t.Error("key1 should have been evicted")
	}
	if !lim.cache.Contains("key2") {
		t.Error("key2 should still be present")
	}
	if !lim.cache.Contains("key3") {
		t.Error("key3 should be present")
	}
	if lim.cache.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", lim.cache.Len())
	}
}

func TestAllow_LRURespectsAccess(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   100,
		AuthBurst:  100,
		MaxEntries: 2,
	})

	// Add two entries
	lim.Allow("key1")
	lim.Allow("key2")

	// Access key1 again, making key2 the LRU
	lim.Allow("key1")

	// Adding a third should evict key2 (LRU)
	lim.Allow("key3")

	if !lim.cache.Contains("key1") {
		t.Error("key1 should still be present (was accessed recently)")
	}
	if lim.cache.Contains("key2") {
		t.Error("key2 should have been evicted (LRU)")
	}
	if !lim.cache.Contains("key3") {
		t.Error("key3 should be present")
	}
}

func TestMiddleware_Returns429(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   0.001, // near-zero refill
		AuthBurst:  1,
		MaxEntries: 100,
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := lim.Middleware(inner)

	// First request should succeed
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "1.2.3.4:5678"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want %d", rec1.Code, http.StatusOK)
	}

	// Second request should be rate-limited
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "1.2.3.4:5678"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got status %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}

	retryAfter := rec2.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("second request: missing Retry-After header")
	}
}

func TestClose_IsNoop(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   10,
		AuthBurst:  10,
		MaxEntries: 100,
	})

	// Close should not panic or return error.
	if err := lim.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}

func TestNew_DefaultsForInvalidConfig(t *testing.T) {
	lim := New(config.RateLimit{
		AuthRate:   -1,
		AuthBurst:  0,
		MaxEntries: 0,
	})

	// Should not panic and should use defaults
	if !lim.Allow("test") {
		t.Error("Allow should succeed with default config values")
	}
}
