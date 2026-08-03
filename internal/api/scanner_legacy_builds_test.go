package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowLegacyScannerBuildsDefaultsEnabledAndDeprecated(t *testing.T) {
	t.Setenv(scannerLegacyBuildsEnvironmentVariable, "")
	called := false
	handler := allowLegacyScannerBuilds(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/legacy", nil))
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("code=%d called=%t", response.Code, called)
	}
	if response.Header().Get("Deprecation") != "true" || response.Header().Get("Link") == "" {
		t.Fatalf("missing legacy endpoint deprecation headers: %#v", response.Header())
	}
}

func TestAllowLegacyScannerBuildsCanBeDisabled(t *testing.T) {
	t.Setenv(scannerLegacyBuildsEnvironmentVariable, "false")
	handler := allowLegacyScannerBuilds(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("disabled legacy handler was called")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/legacy", nil))
	if response.Code != http.StatusGone {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAllowLegacyScannerBuildsRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv(scannerLegacyBuildsEnvironmentVariable, "sometimes")
	handler := allowLegacyScannerBuilds(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid configuration called downstream handler")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/legacy", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}
