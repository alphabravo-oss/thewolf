package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
)

func TestScannerReleaseModeMiddlewareStagesMutations(t *testing.T) {
	t.Setenv(scannerfeature.EnvironmentVariable, "candidate")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	candidate := requireScannerReleaseCapability(scannerfeature.CapabilityCandidate)(next)
	response := httptest.NewRecorder()
	candidate.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("candidate response = %d %s", response.Code, response.Body)
	}

	stable := requireScannerReleaseCapability(scannerfeature.CapabilityStable)(next)
	response = httptest.NewRecorder()
	stable.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("stable response = %d %s", response.Code, response.Body)
	}
}

func TestScannerReleaseModeMiddlewareRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv(scannerfeature.EnvironmentVariable, "invalid")
	handler := requireScannerReleaseCapability(scannerfeature.CapabilityRead)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d %s", response.Code, response.Body)
	}
}
