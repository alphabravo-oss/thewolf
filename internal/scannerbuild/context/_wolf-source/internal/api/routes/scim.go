package routes

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

// SCIMUnavailable is the Community SCIM surface. It 404s unless
// enterprise.identity is granted and the overlay registered a handler.
func SCIMUnavailable(w http.ResponseWriter, r *http.Request) {
	if !entitlement.Active().Allows(entitlement.Identity) {
		response.WriteError(w, http.StatusNotFound, "scim_disabled", "SCIM requires enterprise.identity")
		return
	}
	if svc, ok := edition.Default.Service("enterprise.scim"); ok {
		if h, ok := svc.(http.Handler); ok && h != nil {
			h.ServeHTTP(w, r)
			return
		}
	}
	response.WriteError(w, http.StatusNotImplemented, "scim_unconfigured", "SCIM is not configured")
}
