// Package middleware provides HTTP middleware for the Wolf API server.
package middleware

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// RateLimiter implements a per-IP token-bucket rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*bucket
	rate     int           // tokens added per interval
	burst    int           // max tokens (bucket size)
	interval time.Duration // refill interval
	cleanup  time.Duration // evict stale entries after this
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter that allows `rate` requests per
// `interval` with a burst capacity of `burst`.
func NewRateLimiter(rate, burst int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     rate,
		burst:    burst,
		interval: interval,
		cleanup:  3 * time.Minute,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.cleanup)
		for ip, b := range rl.visitors {
			if b.lastSeen.Before(cutoff) {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	ok, _, _ := rl.take(ip)
	return ok
}

func (rl *RateLimiter) take(ip string) (ok bool, remaining, limit int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	limit = rl.burst

	now := time.Now()
	b, exists := rl.visitors[ip]
	if !exists {
		b = &bucket{limiter: rate.NewLimiter(rate.Limit(float64(rl.rate)/rl.interval.Seconds()), rl.burst)}
		rl.visitors[ip] = b
	}
	b.lastSeen = now
	ok = b.limiter.AllowN(now, 1)
	return ok, int(b.limiter.TokensAt(now)), limit
}

func setRateHeaders(w http.ResponseWriter, remaining, limit int) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
}

// Handler returns middleware that rate-limits requests by client IP.
func (rl *RateLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		ok, rem, limit := rl.take(ip)
		setRateHeaders(w, rem, limit)
		if !ok {
			wolflog.Warn().Str("ip", ip).Str("path", r.URL.Path).Msg("rate limit exceeded")
			w.Header().Set("Retry-After", "1")
			response.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, please try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitRemoteKey(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil && host != "" {
		return host
	}
	return remoteAddr
}

// StrictHandler returns middleware with tighter limits, suitable for
// authentication endpoints.
func StrictRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 5, time.Second) // 5 burst, 1/s refill
}

// DefaultRateLimiter returns a limiter for general API traffic.
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(30, 180, time.Second) // 180 burst, 30/s refill
}

// TokenRateLimiter returns a limiter sized for automation traffic. API
// tokens (CLI / CI / AI agents) are bursty by nature, so they get a higher
// ceiling than browser traffic — and a bucket keyed per token so one noisy
// token cannot exhaust another's quota.
func TokenRateLimiter() *RateLimiter {
	return NewRateLimiter(50, 300, time.Second) // 300 burst, 50/s refill
}

// HandlerForToken rate-limits token-authenticated requests, keyed by token
// ID. Requests authenticated by JWT (no token ID) pass through untouched —
// the general IP limiter already covers them.
func (rl *RateLimiter) HandlerForToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenID := auth.TokenIDFromContext(r.Context())
		if tokenID == "" {
			next.ServeHTTP(w, r)
			return
		}
		ok, rem, limit := rl.take("tok:" + tokenID)
		setRateHeaders(w, rem, limit)
		if !ok {
			wolflog.Warn().Str("token_id", tokenID).Str("path", r.URL.Path).Msg("token rate limit exceeded")
			w.Header().Set("Retry-After", "1")
			response.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
