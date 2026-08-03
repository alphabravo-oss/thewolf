package api

import (
	"net/http"
	"os"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
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
