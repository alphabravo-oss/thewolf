package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alphabravocompany/thewolf/internal/auth"
)

func TestRateLimiterRecoveryDuringTraffic(t *testing.T) {
	for _, token := range []bool{false, true} {
		name := "IP"
		if token {
			name = "token"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// No cleanup goroutine is needed inside a virtual-time test.
				rl := &RateLimiter{visitors: map[string]*bucket{}, rate: 1, burst: 2, interval: time.Second}
				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
				handler := rl.Handler(next)
				if token {
					handler = rl.HandlerForToken(next)
				}
				request := func(id string, want int) {
					t.Helper()
					r := httptest.NewRequest("GET", "/", nil)
					r.RemoteAddr = id + ":1234"
					if token {
						r = r.WithContext(context.WithValue(r.Context(), auth.AuthInfoContextKey, &auth.AuthInfo{TokenID: id}))
					}
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != want {
						t.Fatalf("status=%d want=%d", w.Code, want)
					}
					if w.Header().Get("X-RateLimit-Limit") != "2" {
						t.Fatal("missing burst limit")
					}
				}
				request("10.0.0.1", 204)
				request("10.0.0.1", 204)
				request("10.0.0.1", 429)
				for i := 1; i <= 12; i++ {
					time.Sleep(250 * time.Millisecond)
					want := 429
					if i%4 == 0 {
						want = 204
					}
					request("10.0.0.1", want)
				}
				request("10.0.0.2", 204)
				time.Sleep(10 * time.Second)
				request("10.0.0.1", 204)
				request("10.0.0.1", 204)
				request("10.0.0.1", 429)
			})
		})
	}
}

func TestRateLimiterConcurrentBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &RateLimiter{visitors: map[string]*bucket{}, rate: 1, burst: 5, interval: time.Second}
		var accepted atomic.Int32
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				if rl.allow("same-client") {
					accepted.Add(1)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 5 {
			t.Fatalf("accepted=%d want=5", accepted.Load())
		}
	})
}
