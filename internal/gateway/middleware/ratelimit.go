package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// bucket is a simple token bucket (refill rate + burst).
type bucket struct {
	tokens     float64
	max        float64
	ratePerSec float64
	last       time.Time
	mu         sync.Mutex
}

func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.ratePerSec
		if b.tokens > b.max {
			b.tokens = b.max
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RateLimiter implements token bucket rate limiting per client IP.
type RateLimiter struct {
	rps    float64
	burst  int
	buckets sync.Map // string -> *bucket
}

// NewRateLimiter creates a limiter with the given sustained RPS and burst size.
// If rps <= 0, Wrap becomes a no-op (rate limiting disabled).
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if rps <= 0 {
		return &RateLimiter{rps: 0, burst: 0}
	}
	if burst < 1 {
		burst = int(rps)
		if burst < 1 {
			burst = 1
		}
	}
	return &RateLimiter{rps: rps, burst: burst}
}

func (r *RateLimiter) getBucket(ip string) *bucket {
	if v, ok := r.buckets.Load(ip); ok {
		return v.(*bucket)
	}
	b := &bucket{
		tokens:     float64(r.burst),
		max:        float64(r.burst),
		ratePerSec: r.rps,
		last:       time.Now(),
	}
	actual, _ := r.buckets.LoadOrStore(ip, b)
	return actual.(*bucket)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Wrap applies per-IP rate limiting before invoking next.
func (r *RateLimiter) Wrap(next http.Handler) http.Handler {
	if r == nil || r.rps <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip := clientIP(req)
		if !r.getBucket(ip).allow(time.Now()) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, req)
	})
}
