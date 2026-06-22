// Package middleware provides HTTP middleware for the Wolf API server.
package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

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
	tokens   int
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
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.visitors[ip]
	if !ok {
		rl.visitors[ip] = &bucket{tokens: rl.burst - 1, lastSeen: now}
		return true
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastSeen)
	refill := int(elapsed/rl.interval) * rl.rate
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
	}
	b.lastSeen = now

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

// Handler returns middleware that rate-limits requests by client IP.
func (rl *RateLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := rateLimitRemoteKey(r.RemoteAddr)

		if !rl.allow(ip) {
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
	return NewRateLimiter(10, 60, time.Second) // 60 burst, 10/s refill
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
		if !rl.allow("tok:" + tokenID) {
			wolflog.Warn().Str("token_id", tokenID).Str("path", r.URL.Path).Msg("token rate limit exceeded")
			w.Header().Set("Retry-After", "1")
			response.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
