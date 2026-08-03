package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScanStreamLimiterEnforcesPerScanLimitAndCleansUp(t *testing.T) {
	limiter := newScanStreamLimiter()
	releaseFirst, ok := limiter.acquire("user-1", "scan-1")
	if !ok {
		t.Fatal("first stream was rejected")
	}
	releaseSecond, ok := limiter.acquire("user-1", "scan-1")
	if !ok {
		t.Fatal("second stream was rejected")
	}
	if _, ok := limiter.acquire("user-1", "scan-1"); ok {
		t.Fatal("per-scan stream limit was not enforced")
	}

	releaseFirst()
	releaseFirst() // release must be idempotent
	releaseReplacement, ok := limiter.acquire("user-1", "scan-1")
	if !ok {
		t.Fatal("released stream capacity was not reusable")
	}
	releaseSecond()
	releaseReplacement()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.byUser) != 0 || len(limiter.byScan) != 0 {
		t.Fatalf("stream counters leaked after cleanup: users=%v scans=%v",
			limiter.byUser, limiter.byScan)
	}
}

func TestScanStreamLimiterEnforcesPerUserLimit(t *testing.T) {
	limiter := newScanStreamLimiter()
	releases := make([]func(), 0, maxDurableScanStreamsPerUser)
	for i := 0; i < maxDurableScanStreamsPerUser; i++ {
		release, ok := limiter.acquire("user-1", fmt.Sprintf("scan-%d", i))
		if !ok {
			t.Fatalf("stream %d was rejected before per-user limit", i)
		}
		releases = append(releases, release)
	}
	if _, ok := limiter.acquire("user-1", "one-scan-too-many"); ok {
		t.Fatal("per-user stream limit was not enforced")
	}
	for _, release := range releases {
		release()
	}
}

func TestDurableScanStreamReturnsTooManyRequestsAtQuota(t *testing.T) {
	limiter := newScanStreamLimiter()
	releases := make([]func(), 0, maxDurableScanStreamsPerScan)
	for i := 0; i < maxDurableScanStreamsPerScan; i++ {
		release, ok := limiter.acquire("user-1", "scan-1")
		if !ok {
			t.Fatalf("failed to reserve stream %d", i)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	request := httptest.NewRequest(http.MethodGet, "/api/scans/scan-1/stream", nil)
	response := httptest.NewRecorder()
	streamDurableScanEvents(response, request, &Handler{streamLimiter: limiter}, "scan-1", "user-1")

	if response.Code != http.StatusTooManyRequests ||
		response.Header().Get("Retry-After") != "5" ||
		!strings.Contains(response.Body.String(), `"code":"stream_limit"`) {
		t.Fatalf("quota response = %d headers=%v body=%s",
			response.Code, response.Header(), response.Body.String())
	}
}
