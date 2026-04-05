package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIP_XForwardedFor(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		wantIP   string
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
	cfg := Config{
		Rate:       10,
		Burst:      5,
		MaxEntries: 100,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)
	defer lim.Close()

	for i := 0; i < 5; i++ {
		if !lim.Allow("testkey") {
			t.Fatalf("Allow() returned false on request %d, expected true (under burst limit)", i+1)
		}
	}
}

func TestAllow_OverLimit(t *testing.T) {
	cfg := Config{
		Rate:       0.1, // very slow refill
		Burst:      3,
		MaxEntries: 100,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)
	defer lim.Close()

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

func TestAllow_EvictsOldest(t *testing.T) {
	cfg := Config{
		Rate:       100,
		Burst:      100,
		MaxEntries: 2,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)
	defer lim.Close()

	// Add two entries
	lim.Allow("key1")
	lim.Allow("key2")

	// Adding a third should evict the oldest (key1)
	lim.Allow("key3")

	lim.mu.Lock()
	_, hasKey1 := lim.entries["key1"]
	_, hasKey2 := lim.entries["key2"]
	_, hasKey3 := lim.entries["key3"]
	count := len(lim.entries)
	lim.mu.Unlock()

	if hasKey1 {
		t.Error("key1 should have been evicted")
	}
	if !hasKey2 {
		t.Error("key2 should still be present")
	}
	if !hasKey3 {
		t.Error("key3 should be present")
	}
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}
}

func TestMiddleware_Returns429(t *testing.T) {
	cfg := Config{
		Rate:       0.001, // near-zero refill
		Burst:      1,
		MaxEntries: 100,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)
	defer lim.Close()

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

func TestMiddlewareByKey(t *testing.T) {
	cfg := Config{
		Rate:       0.001,
		Burst:      1,
		MaxEntries: 100,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)
	defer lim.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	keyFunc := func(r *http.Request) string {
		return r.Header.Get("X-User-ID")
	}
	mw := lim.MiddlewareByKey(keyFunc)
	handler := mw(inner)

	// First request for user-A: success
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.Header.Set("X-User-ID", "user-A")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("user-A first request: got status %d, want %d", rec1.Code, http.StatusOK)
	}

	// Second request for user-A: rate-limited
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-User-ID", "user-A")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("user-A second request: got status %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}

	// First request for user-B: should succeed (different key)
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.Header.Set("X-User-ID", "user-B")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("user-B first request: got status %d, want %d", rec3.Code, http.StatusOK)
	}
}

func TestMiddlewareByKey_EmptyKeyPassthrough(t *testing.T) {
	l := NewLimiter(Config{Rate: 1, Burst: 1, MaxEntries: 100, CleanupAge: time.Minute})
	defer l.Close()

	mw := l.MiddlewareByKey(func(r *http.Request) string { return "" })
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Multiple requests should all pass since key is empty
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: got %d, want 200", i, rec.Code)
		}
	}
}

func TestClose_StopsCleanup(t *testing.T) {
	cfg := Config{
		Rate:       10,
		Burst:      10,
		MaxEntries: 100,
		CleanupAge: 10 * time.Minute,
	}
	lim := NewLimiter(cfg)

	// Close should not panic or block indefinitely.
	if err := lim.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}
