package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

const scannerLegacyBuildsEnvironmentVariable = "WOLF_SCANNER_LEGACY_BUILD_ENDPOINTS"

// allowLegacyScannerBuilds keeps the established synchronous image-build
// surface on by default for upgrade compatibility. Operators may explicitly
// retire it after every caller has moved to durable custom-build operations.
func allowLegacyScannerBuilds(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(scannerLegacyBuildsEnvironmentVariable)))
		switch value {
		case "", "1", "true", "yes", "on", "enabled":
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Link", `</api/v1/scanners/custom-builds>; rel="successor-version"`)
			next.ServeHTTP(w, r)
		case "0", "false", "no", "off", "disabled":
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Link", `</api/v1/scanners/custom-builds>; rel="successor-version"`)
			response.WriteError(
				w,
				http.StatusGone,
				"legacy_scanner_builds_disabled",
				"legacy scanner image build endpoints are disabled; use durable custom scanner-image builds",
			)
		default:
			response.WriteError(
				w,
				http.StatusServiceUnavailable,
				"legacy_scanner_builds_config_invalid",
				"legacy scanner build endpoint configuration is invalid",
			)
		}
	})
}
