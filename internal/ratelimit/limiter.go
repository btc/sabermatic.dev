// Package ratelimit provides per-IP token-bucket rate limiting for HTTP handlers.
//
// It uses hashicorp/golang-lru for thread-safe LRU eviction of per-IP rate
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

	"github.com/btc/drill/internal/config"
)

// entry holds a per-key rate limiter and the time it was last accessed.
type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-IP rate limiter backed by golang.org/x/time/rate token
// buckets and hashicorp/golang-lru for thread-safe LRU eviction.
// It is safe for concurrent use.
type Limiter struct {
	rate       float64
	burst      int
	maxEntries int
	cache      *lru.Cache[string, *entry]
}

// New creates a Limiter from the application rate limit config.
func New(cfg config.RateLimit) *Limiter {
	r := cfg.AuthRate
	if r <= 0 {
		r = 1
	}
	burst := cfg.AuthBurst
	if burst <= 0 {
		burst = 1
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	cache, _ := lru.New[string, *entry](maxEntries)
	return &Limiter{
		rate:       r,
		burst:      burst,
		maxEntries: maxEntries,
		cache:      cache,
	}
}

// Allow reports whether a request with the given key should be allowed.
// It creates a new token bucket for unseen keys. LRU eviction is handled
// automatically by the cache when MaxEntries is exceeded.
func (l *Limiter) Allow(key string) bool {
	e, ok := l.cache.Get(key)
	if !ok {
		e = &entry{
			limiter:  rate.NewLimiter(rate.Limit(l.rate), l.burst),
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
			retryAfter := fmt.Sprintf("%.0f", 1.0/l.rate)
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
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
