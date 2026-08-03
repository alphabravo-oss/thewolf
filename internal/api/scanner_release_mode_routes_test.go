package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
)

func TestScannerReleaseModesAreEnforcedByMountedRoutes(t *testing.T) {
	t.Run("disabled blocks reads", func(t *testing.T) {
		t.Setenv(scannerfeature.EnvironmentVariable, "disabled")
		srv, _, jwt := newTestServer(t)

		response := scannerReleaseRequest(
			srv, jwt, http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil, nil,
		)
		assertScannerReleaseRestriction(t, response, "disabled", "read")
	})

	t.Run("read only preserves inventory and blocks candidate mutations", func(t *testing.T) {
		t.Setenv(scannerfeature.EnvironmentVariable, "read_only")
		srv, _, jwt := newTestServer(t)

		overview := scannerReleaseRequest(
			srv, jwt, http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil, nil,
		)
		if overview.Code != http.StatusOK {
			t.Fatalf("overview code=%d body=%s", overview.Code, overview.Body.String())
		}

		discovery := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/discovery-runs",
			[]byte(`{"scope":{"type":"all"},"reason":"route mode test"}`),
			map[string]string{"Idempotency-Key": "read-only-discovery"},
		)
		assertScannerReleaseRestriction(t, discovery, "read_only", "candidate")

		legacyImport := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/legacy-release-imports",
			[]byte(`{"reason":"mode boundary test"}`),
			map[string]string{"Idempotency-Key": "read-only-legacy-import"},
		)
		assertScannerReleaseRestriction(t, legacyImport, "read_only", "candidate")
	})

	t.Run("candidate permits discovery and blocks canary publication", func(t *testing.T) {
		t.Setenv(scannerfeature.EnvironmentVariable, "candidate")
		srv, _, jwt := newTestServer(t)

		discovery := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/discovery-runs",
			[]byte(`{"scope":{"type":"all"},"reason":"route mode test"}`),
			map[string]string{"Idempotency-Key": "candidate-discovery"},
		)
		if discovery.Code != http.StatusAccepted {
			t.Fatalf("discovery code=%d body=%s", discovery.Code, discovery.Body.String())
		}

		publish := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/candidates/not-used/publish",
			nil,
			nil,
		)
		assertScannerReleaseRestriction(t, publish, "candidate", "canary")
	})

	t.Run("canary permits rollout commands and blocks stable controls", func(t *testing.T) {
		t.Setenv(scannerfeature.EnvironmentVariable, "canary")
		srv, _, jwt := newTestServer(t)

		promote := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/releases/not-used/promote",
			nil,
			nil,
		)
		if promote.Code != http.StatusPreconditionRequired {
			t.Fatalf("promote should pass mode enforcement and reach command validation: code=%d body=%s", promote.Code, promote.Body.String())
		}

		deprecate := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/releases/not-used/deprecate",
			nil,
			nil,
		)
		assertScannerReleaseRestriction(t, deprecate, "canary", "stable_control")
	})

	t.Run("stable control permits stable commands", func(t *testing.T) {
		t.Setenv(scannerfeature.EnvironmentVariable, "stable_control")
		srv, _, jwt := newTestServer(t)

		deprecate := scannerReleaseRequest(
			srv,
			jwt,
			http.MethodPost,
			"/api/v1/scanner-supply-chain/releases/not-used/deprecate",
			nil,
			nil,
		)
		if deprecate.Code != http.StatusPreconditionRequired {
			t.Fatalf("deprecate should pass mode enforcement and reach command validation: code=%d body=%s", deprecate.Code, deprecate.Body.String())
		}
	})
}

func TestDisabledLegacyScannerBuildRouteReturnsGone(t *testing.T) {
	t.Setenv("WOLF_SCANNER_LEGACY_BUILD_ENDPOINTS", "false")
	srv, _, jwt := newTestServer(t)

	response := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/images/build-all", nil, nil,
	)
	if response.Code != http.StatusGone {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Deprecation") != "true" {
		t.Fatalf("Deprecation header=%q", response.Header().Get("Deprecation"))
	}
	if response.Header().Get("Link") != `</api/v1/scanners/custom-builds>; rel="successor-version"` {
		t.Fatalf("Link header=%q", response.Header().Get("Link"))
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	mustJSON(t, response.Body.Bytes(), &payload)
	if payload.Error.Code != "legacy_scanner_builds_disabled" {
		t.Fatalf("error code=%q", payload.Error.Code)
	}
}

func TestScannerArtifactDiffRoutesRequireAuthenticationAndReadCapability(t *testing.T) {
	t.Setenv(scannerfeature.EnvironmentVariable, "read_only")
	srv, _, jwt := newTestServer(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/scanner-supply-chain/candidates/missing/diffs/manifest",
		nil,
	)
	unauthenticated := httptest.NewRecorder()
	srv.Router.ServeHTTP(unauthenticated, request)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthenticated diff code=%d body=%s",
			unauthenticated.Code,
			unauthenticated.Body.String(),
		)
	}

	authenticated := scannerReleaseRequest(
		srv,
		jwt,
		http.MethodGet,
		"/api/v1/scanner-supply-chain/candidates/missing/diffs/manifest",
		nil,
		nil,
	)
	if authenticated.Code != http.StatusNotFound {
		t.Fatalf(
			"read-only authenticated diff code=%d body=%s",
			authenticated.Code,
			authenticated.Body.String(),
		)
	}
}

func scannerReleaseRequest(
	srv *api.Server,
	bearer string,
	method string,
	path string,
	body []byte,
	headers map[string]string,
) *httptest.ResponseRecorder {
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		requestBody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	srv.Router.ServeHTTP(response, req)
	return response
}

func assertScannerReleaseRestriction(
	t *testing.T,
	response *httptest.ResponseRecorder,
	mode string,
	required string,
) {
	t.Helper()
	if response.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code               string `json:"code"`
			Mode               string `json:"mode"`
			RequiredCapability string `json:"required_capability"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, response.Body.String())
	}
	if payload.Error.Code != "scanner_release_mode_restricted" ||
		payload.Error.Mode != mode ||
		payload.Error.RequiredCapability != required {
		t.Fatalf("restriction=%+v", payload.Error)
	}
}
