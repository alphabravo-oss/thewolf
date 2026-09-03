package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
	"github.com/alphabravocompany/thewolf/pkg/license"
	"github.com/alphabravocompany/thewolf/pkg/pluginapi"
)

type licenseBlobRequest struct {
	License string `json:"license"`
}

func licenseVerifier() license.Verifier {
	if svc, ok := edition.Default.Service("license.verifier"); ok {
		if v, ok := svc.(license.Verifier); ok && v != nil {
			return v
		}
	}
	return license.Community{Edition: edition.Default.Name()}
}

func licenseStatus(blob string) license.Status {
	st := licenseVerifier().Inspect(blob)
	if st.Edition == "" {
		st.Edition = edition.Default.Name()
	}
	st.DataIntact = true
	return st
}

func init() {
	_ = edition.Default.Add(edition.CommunityModule{})
}

func visibleUIRoutes(checker entitlement.Checker) []edition.UIRoute {
	all := edition.Default.UI().Routes
	enterpriseOn := false
	for _, c := range entitlement.Catalog() {
		if strings.HasPrefix(c, "enterprise.") && checker.Allows(c) {
			enterpriseOn = true
			break
		}
	}
	if enterpriseOn {
		return all
	}
	out := make([]edition.UIRoute, 0, len(all))
	for _, rt := range all {
		if !strings.HasPrefix(rt.Path, "/enterprise/") {
			out = append(out, rt)
		}
	}
	return out
}

func mcpEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_MCP_ENABLED")))
	return v == "1" || v == "true" || v == "yes"
}

// GetEdition handles GET /api/edition — public edition and entitlement status.
func GetEdition(w http.ResponseWriter, r *http.Request) {
	checker := entitlement.Active()
	granted := map[string]bool{}
	for _, c := range entitlement.Catalog() {
		granted[c] = checker.Allows(c)
	}
	iso := strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCAN_ISOLATION")))
	if iso == "" {
		iso = "standard"
	}
	netw := strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCANNERS_NETWORK")))
	if netw == "" {
		netw = "none"
	}
	w.Header().Set("ETag", `"`+edition.ContractVersion+`"`)
	ed := edition.Default.Name()
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]any{
			"edition":          ed,
			"product":          edition.Product(ed),
			"licensed":         entitlement.Licensed(),
			"contract_version": edition.ContractVersion,
			"modules":          edition.Default.Modules(),
			"entitlements":     granted,
			"limits":           entitlement.CommunityLimits(),
			"mcp":              map[string]any{"enabled": mcpEnabled()},
			"ui_routes":        visibleUIRoutes(checker),
			"isolation":        map[string]any{"profile": iso, "scanner_network": netw},
			"plugin_kinds":     pluginapi.Kinds(),
		},
	})
}

// GetLicense handles GET /api/license — Community has no commercial license.
func GetLicense(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: licenseStatus(""),
	})
}

// ValidateLicense handles POST /license/validate. Community never reports valid.
func ValidateLicense(w http.ResponseWriter, r *http.Request) {
	var req licenseBlobRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: licenseStatus(req.License),
	})
}

// InstallLicense handles POST /license/install (admin). Community rejects
// without storing the blob or claiming verification.
func InstallLicense(w http.ResponseWriter, r *http.Request) {
	var req licenseBlobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.License) == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "license is required")
		return
	}
	err := licenseVerifier().Install(req.License)
	if errors.Is(err, license.ErrCommunityBinary) {
		response.WriteError(w, http.StatusConflict, "community_binary", license.ReasonCommunity)
		return
	}
	if errors.Is(err, license.ErrUnconfigured) {
		response.WriteError(w, http.StatusNotImplemented, "license_unconfigured", err.Error())
		return
	}
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "license_invalid", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: licenseStatus(req.License)})
}
