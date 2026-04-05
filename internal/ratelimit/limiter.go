// Package ratelimit provides per-key token-bucket rate limiting for HTTP handlers.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config controls the rate limiter behaviour.
type Config struct {
	// Rate is the number of requests per second allowed per key.
	Rate float64
	// Burst is the maximum burst size per key.
	Burst int
	// MaxEntries caps the number of tracked keys. When exceeded, the oldest
	// entry is evicted.
	MaxEntries int
	// CleanupAge is the duration after which idle entries are evicted by the
	// background cleanup goroutine.
	CleanupAge time.Duration
}

// entry holds a per-key rate limiter and the time it was last accessed.
type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-key rate limiter backed by golang.org/x/time/rate token
// buckets. It is safe for concurrent use.
type Limiter struct {
	cfg       Config
	mu        sync.Mutex
	entries   map[string]*entry
	done      chan struct{}
	closeOnce sync.Once
}

// NewLimiter creates a Limiter and starts a background goroutine that evicts
// stale entries every 5 minutes. Call Close to stop the goroutine.
func NewLimiter(cfg Config) *Limiter {
	l := &Limiter{
		cfg:     cfg,
		entries: make(map[string]*entry),
		done:    make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

// Allow reports whether a request with the given key should be allowed.
// It creates a new token bucket for unseen keys and evicts the oldest entry
// when MaxEntries is exceeded.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok {
		// Evict least-recently-seen entry if at capacity.
		if l.cfg.MaxEntries > 0 && len(l.entries) >= l.cfg.MaxEntries {
			l.evictOldestLocked()
		}
		e = &entry{
			limiter:  rate.NewLimiter(rate.Limit(l.cfg.Rate), l.cfg.Burst),
			lastSeen: time.Now(),
		}
		l.entries[key] = e
	}

	e.lastSeen = time.Now()
	return e.limiter.Allow()
}

// evictOldestLocked removes the entry with the earliest lastSeen timestamp (LRU).
// Caller must hold l.mu.
func (l *Limiter) evictOldestLocked() {
	var oldestKey string
	var oldestSeen time.Time
	first := true
	for k, e := range l.entries {
		if first || e.lastSeen.Before(oldestSeen) {
			oldestKey = k
			oldestSeen = e.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}

// Middleware returns an http.Handler that rate-limits requests by client IP.
// Requests that exceed the rate limit receive a 429 Too Many Requests response
// with a Retry-After header.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ClientIP(r)
		if !l.Allow(key) {
			retryAfter := fmt.Sprintf("%.0f", 1.0/l.cfg.Rate)
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MiddlewareByKey returns middleware that rate-limits requests using a
// caller-supplied key function. This is useful for user-keyed limiting where
// the user ID is extracted from the request context after authentication.
func (l *Limiter) MiddlewareByKey(keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if key == "" {
				// No key available — pass through without limiting.
				next.ServeHTTP(w, r)
				return
			}
			if !l.Allow(key) {
				retryAfter := fmt.Sprintf("%.0f", 1.0/l.cfg.Rate)
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Close stops the background cleanup goroutine. It implements io.Closer.
// Safe to call multiple times.
func (l *Limiter) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

// cleanupLoop runs every 5 minutes and evicts entries that haven't been seen
// in CleanupAge.
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			l.cleanup()
		}
	}
}

// cleanup evicts entries older than CleanupAge.
func (l *Limiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.cfg.CleanupAge)
	for k, e := range l.entries {
		if e.lastSeen.Before(cutoff) {
			delete(l.entries, k)
		}
	}
}

// ClientIP extracts the client IP address from the request. It uses the
// leftmost IP from X-Forwarded-For if present, falling back to RemoteAddr
// with the port stripped.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the leftmost (client) IP.
		if i := strings.IndexByte(xff, ','); i != -1 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	// Fallback to RemoteAddr, stripping the port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port.
		return r.RemoteAddr
	}
	return host
}
