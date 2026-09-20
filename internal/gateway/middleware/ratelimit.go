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
	lastSeen   time.Time
	mu         sync.Mutex
}

func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastSeen = now
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
// Buckets are reaped after bucketTTL to bound memory. X-Forwarded-For is only
// honored when the direct peer is a configured trusted proxy.
type RateLimiter struct {
	rps       float64
	burst     int
	trusted   map[string]bool
	bucketTTL time.Duration
	buckets   sync.Map // string -> *bucket

	lastSweep time.Time
	sweepMu   sync.Mutex
}

// NewRateLimiter creates a limiter with the given sustained RPS and burst size.
// If rps <= 0, Wrap becomes a no-op (rate limiting disabled). trustedProxies lists
// peer addresses (e.g. "127.0.0.1") whose X-Forwarded-For header may be trusted.
func NewRateLimiter(rps float64, burst int, trustedProxies []string) *RateLimiter {
	if rps <= 0 {
		return &RateLimiter{rps: 0, burst: 0}
	}
	if burst < 1 {
		burst = int(rps)
		if burst < 1 {
			burst = 1
		}
	}
	trusted := make(map[string]bool, len(trustedProxies))
	for _, p := range trustedProxies {
		p = strings.TrimSpace(p)
		if p != "" {
			trusted[p] = true
		}
	}
	return &RateLimiter{
		rps:       rps,
		burst:     burst,
		trusted:   trusted,
		bucketTTL: 15 * time.Minute,
	}
}

// getBucket returns the bucket for ip, creating it on first use and lazily
// sweeping stale buckets to keep memory bounded.
func (r *RateLimiter) getBucket(ip string) *bucket {
	r.maybeSweep(time.Now())
	if v, ok := r.buckets.Load(ip); ok {
		return v.(*bucket)
	}
	b := &bucket{
		tokens:     float64(r.burst),
		max:        float64(r.burst),
		ratePerSec: r.rps,
		last:       time.Now(),
		lastSeen:   time.Now(),
	}
	actual, _ := r.buckets.LoadOrStore(ip, b)
	return actual.(*bucket)
}

// maybeSweep removes buckets not seen within bucketTTL. It runs at most once per
// bucketTTL interval and never blocks the hot path for long.
func (r *RateLimiter) maybeSweep(now time.Time) {
	if r.bucketTTL <= 0 {
		return
	}
	r.sweepMu.Lock()
	defer r.sweepMu.Unlock()
	if now.Sub(r.lastSweep) < r.bucketTTL {
		return
	}
	r.lastSweep = now
	r.buckets.Range(func(k, v interface{}) bool {
		b := v.(*bucket)
		b.mu.Lock()
		stale := now.Sub(b.lastSeen) > r.bucketTTL
		b.mu.Unlock()
		if stale {
			r.buckets.Delete(k)
		}
		return true
	})
}

// clientIP returns the client address used for rate limiting. X-Forwarded-For is
// only honored when the direct peer is a trusted proxy; otherwise it is ignored to
// prevent spoofing.
func clientIP(r *http.Request, trusted map[string]bool) string {
	peer := hostFromRemoteAddr(r.RemoteAddr)
	if len(trusted) > 0 && trusted[peer] {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
	}
	return peer
}

func hostFromRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// Wrap applies per-IP (and, when an API key is presented, per-key) rate limiting
// before invoking next.
func (r *RateLimiter) Wrap(next http.Handler) http.Handler {
	if r == nil || r.rps <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := clientKey(req, r.trusted)
		if !r.getBucket(key).allow(time.Now()) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// clientKey returns the rate-limit bucket key. When an API key is presented it is
// included so each key (tenant) gets an independent bucket.
func clientKey(r *http.Request, trusted map[string]bool) string {
	ip := clientIP(r, trusted)
	if k := apiKeyFromRequest(r); k != "" {
		return "key:" + k + "|ip:" + ip
	}
	return ip
}

// apiKeyFromRequest extracts an API key from X-API-Key or a Bearer token.
func apiKeyFromRequest(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}
