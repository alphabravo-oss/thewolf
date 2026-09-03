package routes

import (
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

const coverageHonesty = "Depth is the federated scanner's own analysis. Wolf does not claim CPG, reachability, or proprietary semantic depth from this matrix."

// GetCoverage handles GET /api/coverage — honest scanner matrix from tools.yaml.
func GetCoverage(w http.ResponseWriter, r *http.Request) {
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load scanner manifest")
		return
	}
	names := make([]string, 0, len(m.Tools))
	for name := range m.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		t := m.Tools[name]
		tools = append(tools, map[string]any{
			"name":             name,
			"display_name":     t.DisplayName,
			"category":         t.Category,
			"integration_tier": t.IntegrationTier,
			"network_required": t.NetworkRequired,
			"pinned_version":   t.PinnedVersion,
			"parser_contract":  t.ParserContract.Format != "" || len(t.ParserContract.Fixtures) > 0,
			"depth":            "federated-scanner",
		})
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]any{
			"source":     "scanners/tools.yaml",
			"tool_count": len(tools),
			"honesty":    coverageHonesty,
			"tools":      tools,
		},
	})
}

// GetCapability handles GET /api/capabilities/{name}. Community 404s
// enterprise.* and cloud.*; overlay reports configured services.
func GetCapability(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" || !entitlement.Active().Allows(name) {
		response.WriteError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	svc, ok := edition.Default.Service(name)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]any{
			"capability": name,
			"granted":    true,
			"configured": ok && svc != nil,
		},
	})
}
