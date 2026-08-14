package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMaxBodySize_AllowsSmallBody(t *testing.T) {
	handler := MaxBodySize(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("unexpected error reading body: %v", err)
			http.Error(w, "read error", 500)
			return
		}
		w.Write(body)
	}))

	req := httptest.NewRequest("POST", "/", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", w.Body.String())
	}
}

func TestMaxBodySize_RejectsLargeBody(t *testing.T) {
	handler := MaxBodySize(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// MaxBytesReader returns an error when limit is exceeded
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", strings.NewReader("this body is way too large"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}
}

func TestMaxBodySize_NilBody(t *testing.T) {
	handler := MaxBodySize(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

func TestMaxBodySizeForRequest_UsesOnlyExplicitOverride(t *testing.T) {
	handler := MaxBodySizeForRequest(5, func(r *http.Request) int64 {
		if r.URL.Path == "/large-upload" {
			return 20
		}
		return 0
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}))

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, httptest.NewRequest(http.MethodPost, "/large-upload", strings.NewReader("1234567890")))
	if allowed.Code != http.StatusOK || allowed.Body.String() != "1234567890" {
		t.Fatalf("override response = %d %q", allowed.Code, allowed.Body.String())
	}

	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/ordinary", strings.NewReader("1234567890")))
	if rejected.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("default response = %d %q", rejected.Code, rejected.Body.String())
	}
}

func TestRecoverMaxBytesError_PassesThrough(t *testing.T) {
	handler := RecoverMaxBytesError(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestRecoverMaxBytesError_CatchesMaxBytesError(t *testing.T) {
	handler := RecoverMaxBytesError(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(&http.MaxBytesError{Limit: 100})
	}))

	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}

	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if errResp.Error.Code != "payload_too_large" {
		t.Errorf("expected error code payload_too_large, got %q", errResp.Error.Code)
	}
}

func TestRecoverMaxBytesError_RePanicsOtherErrors(t *testing.T) {
	handler := RecoverMaxBytesError(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("some other error")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected re-panic for non-MaxBytesError, got nil")
		}
		if r != "some other error" {
			t.Errorf("expected panic value 'some other error', got %v", r)
		}
	}()

	handler.ServeHTTP(w, req)
}

func TestRateLimiter_AllowsBurst(t *testing.T) {
	rl := NewRateLimiter(1, 3, time.Second)

	for i := 0; i < 3; i++ {
		if !rl.allow("192.168.1.1") {
			t.Errorf("request %d should have been allowed within burst", i+1)
		}
	}

	// Fourth request should be denied
	if rl.allow("192.168.1.1") {
		t.Error("request beyond burst should have been denied")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter(1, 2, time.Second)

	// Exhaust ip1
	rl.allow("ip1")
	rl.allow("ip1")
	if rl.allow("ip1") {
		t.Error("ip1 should be rate limited")
	}

	// ip2 should still have its own bucket
	if !rl.allow("ip2") {
		t.Error("ip2 should not be rate limited")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewRateLimiter(1, 2, 50*time.Millisecond)

	// Exhaust burst
	rl.allow("test")
	rl.allow("test")
	if rl.allow("test") {
		t.Error("should be rate limited after burst")
	}

	// Wait for refill
	time.Sleep(60 * time.Millisecond)

	if !rl.allow("test") {
		t.Error("should be allowed after refill interval")
	}
}

func TestRateLimiter_Handler_Returns429(t *testing.T) {
	rl := NewRateLimiter(1, 1, time.Second)

	handler := rl.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should pass
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", w.Code)
	}

	// Second request should be rate limited
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After header, got %q", w.Header().Get("Retry-After"))
	}
}

func TestRateLimiter_Handler_IgnoresXForwardedFor(t *testing.T) {
	rl := NewRateLimiter(1, 1, time.Second)

	handler := rl.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with X-Forwarded-For consumes only the real remote bucket.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// A spoofed same forwarded IP from a different RemoteAddr must not share
	// the bucket. Wolf should only trust RemoteAddr unless a trusted-proxy
	// layer has already rewritten it.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "10.0.0.2:5678"
	req2.Header.Set("X-Forwarded-For", "203.0.113.50")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 for different RemoteAddr despite same X-Forwarded-For, got %d", w2.Code)
	}
}

func TestStrictRateLimiter(t *testing.T) {
	rl := StrictRateLimiter()
	if rl == nil {
		t.Fatal("StrictRateLimiter returned nil")
	}
	if rl.burst != 5 {
		t.Errorf("expected burst=5, got %d", rl.burst)
	}
	if rl.rate != 1 {
		t.Errorf("expected rate=1, got %d", rl.rate)
	}
}

func TestDefaultRateLimiter(t *testing.T) {
	rl := DefaultRateLimiter()
	if rl == nil {
		t.Fatal("DefaultRateLimiter returned nil")
	}
	if rl.burst != 180 {
		t.Errorf("expected burst=180, got %d", rl.burst)
	}
	if rl.rate != 30 {
		t.Errorf("expected rate=30, got %d", rl.rate)
	}
}
