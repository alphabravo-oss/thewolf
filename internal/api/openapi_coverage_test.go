package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/openapi"
)

// TestOpenAPICoversEveryRoute walks the live chi router and asserts that
// every registered /api/v1 endpoint is documented in the OpenAPI spec.
// An undocumented endpoint fails the build — the spec cannot drift.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	srv, _, _ := newTestServer(t)

	documented := map[string]bool{}
	for _, ep := range openapi.Endpoints() {
		documented[ep.Method+" /api/v1"+ep.Path] = true
	}

	// Endpoints that are intentionally not in the operation catalog.
	exempt := map[string]bool{
		"GET /api/v1/openapi.json":  true,
		"GET /api/v1/docs":          true,
		"GET /api/v1/docs/redoc":    true,
		"GET /api/v1/docs/static/*": true,
	}

	walkErr := chi.Walk(srv.Router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if !strings.HasPrefix(route, "/api/v1/") {
			return nil
		}
		key := method + " " + route
		if exempt[key] || documented[key] {
			return nil
		}
		t.Errorf("route %s is registered but missing from the OpenAPI spec", key)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk: %v", walkErr)
	}
}

// TestOpenAPIEndpointsAreAllRegistered is the reverse check: every endpoint
// the spec advertises must actually exist on the router.
func TestOpenAPIEndpointsAreAllRegistered(t *testing.T) {
	srv, _, _ := newTestServer(t)

	registered := map[string]bool{}
	_ = chi.Walk(srv.Router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	for _, ep := range openapi.Endpoints() {
		key := ep.Method + " /api/v1" + ep.Path
		if !registered[key] {
			t.Errorf("spec advertises %s but no such route is registered", key)
		}
	}
}
