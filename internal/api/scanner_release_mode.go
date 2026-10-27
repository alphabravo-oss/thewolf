package api

import (
	"net/http"
	"os"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

func requireScannerReleaseCapability(required scannerfeature.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mode, err := scannerfeature.Parse(os.Getenv(scannerfeature.EnvironmentVariable))
			if err != nil {
				response.WriteError(
					w, http.StatusServiceUnavailable, "scanner_release_mode_invalid",
					"scanner release management mode is invalid",
				)
				return
			}
			if !mode.Allows(required) {
				response.WriteJSON(w, http.StatusConflict, map[string]any{
					"error": map[string]any{
						"code":                "scanner_release_mode_restricted",
						"message":             "operation is unavailable in the current scanner release management mode",
						"mode":                mode,
						"required_capability": required,
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireEntitlement answers 403 enterprise_required for a surface the active
// edition does not grant. Enterprise-only features stay discoverable (the UI
// reads the same entitlements) but are never executable from Community.
func requireEntitlement(capability, feature string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !entitlement.Active().Allows(capability) {
				response.WriteError(w, http.StatusForbidden, "enterprise_required", feature+" requires Wolf Enterprise")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
