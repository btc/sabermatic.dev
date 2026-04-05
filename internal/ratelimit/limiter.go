// Package ratelimit provides per-key token-bucket rate limiting for HTTP handlers.
//
// It uses hashicorp/golang-lru for thread-safe LRU eviction of per-key rate
// limiters, eliminating manual mutex management, eviction code, and background
// cleanup goroutines.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/time/rate"
)

// Config controls the rate limiter behaviour.
type Config struct {
	// Rate is the number of requests per second allowed per key.
	Rate float64
	// Burst is the maximum burst size per key.
	Burst int
	// MaxEntries caps the number of tracked keys. When exceeded, the
	// least-recently-used entry is evicted automatically by the LRU cache.
	MaxEntries int
}

// entry holds a per-key rate limiter and the time it was last accessed.
type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-key rate limiter backed by golang.org/x/time/rate token
// buckets and hashicorp/golang-lru for thread-safe LRU eviction.
// It is safe for concurrent use.
type Limiter struct {
	cfg   Config
	cache *lru.Cache[string, *entry]
}

// NewLimiter creates a Limiter with an LRU cache of size MaxEntries.
func NewLimiter(cfg Config) *Limiter {
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 1000
	}
	cache, _ := lru.New[string, *entry](cfg.MaxEntries)
	return &Limiter{
		cfg:   cfg,
		cache: cache,
	}
}

// Allow reports whether a request with the given key should be allowed.
// It creates a new token bucket for unseen keys. LRU eviction is handled
// automatically by the cache when MaxEntries is exceeded.
func (l *Limiter) Allow(key string) bool {
	e, ok := l.cache.Get(key)
	if !ok {
		e = &entry{
			limiter:  rate.NewLimiter(rate.Limit(l.cfg.Rate), l.cfg.Burst),
			lastSeen: time.Now(),
		}
		l.cache.Add(key, e)
	}
	e.lastSeen = time.Now()
	return e.limiter.Allow()
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
				// No key available -- pass through without limiting.
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

// Close is a no-op that satisfies io.Closer. The hashicorp LRU cache does
// not require cleanup. Retained for interface compatibility.
func (l *Limiter) Close() error {
	return nil
}

// ClientIP extracts the client IP from the request.
// TRUST ASSUMPTION: The service runs behind a reverse proxy that sets
// X-Forwarded-For. If clients can connect directly, they can spoof this
// header and bypass per-IP rate limiting. In direct-connect deployments,
// use RemoteAddr only.
//
// It uses the leftmost IP from X-Forwarded-For if present, falling back to
// RemoteAddr with the port stripped.
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
