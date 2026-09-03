package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
	"github.com/alphabravocompany/thewolf/pkg/intelligence"
	"github.com/alphabravocompany/thewolf/pkg/verification"
)

func GetAttackPath(w http.ResponseWriter, r *http.Request) {
	in, ok := loadIntelInput(w, r)
	if !ok {
		return
	}
	c, ok := correlator()
	if !ok {
		response.WriteError(w, http.StatusNotImplemented, "intelligence_unconfigured", "attack paths are not configured")
		return
	}
	path, err := c.AttackPath(r.Context(), in)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "intelligence_error", "failed to build attack path")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: path})
}

func InvestigateVulnerability(w http.ResponseWriter, r *http.Request) {
	in, ok := loadIntelInput(w, r)
	if !ok {
		return
	}
	var req struct {
		Question string `json:"question"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	in.Question = req.Question
	c, ok := correlator()
	if !ok {
		response.WriteError(w, http.StatusNotImplemented, "intelligence_unconfigured", "investigation is not configured")
		return
	}
	out, err := c.Investigate(r.Context(), in)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "intelligence_error", "investigation failed")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
}

func VerifyVulnerability(w http.ResponseWriter, r *http.Request) {
	v, ok := loadVulnForIntel(w, r, entitlement.Verification)
	if !ok {
		return
	}
	var req verification.Request
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.VulnerabilityID = v.ID
	eng, ok := verifyEngine()
	if !ok {
		response.WriteError(w, http.StatusNotImplemented, "verification_unconfigured", "runtime verification is not configured")
		return
	}
	out, err := eng.Verify(r.Context(), req)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "verification_error", "verification failed")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
}

func loadIntelInput(w http.ResponseWriter, r *http.Request) (intelligence.Input, bool) {
	v, ok := loadVulnForIntel(w, r, entitlement.Intelligence)
	if !ok {
		return intelligence.Input{}, false
	}
	in := intelligence.Input{VulnerabilityID: v.ID, Title: v.Title}
	for _, e := range v.Evidence {
		in.Evidence = append(in.Evidence, intelligence.EvidenceRef{
			FindingID: e.FindingID,
			Tool:      e.ToolName,
			Title:     e.Title,
			File:      e.FilePath,
			Line:      e.LineStart,
			Reason:    e.Reason,
		})
	}
	return in, true
}

func loadVulnForIntel(w http.ResponseWriter, r *http.Request, cap string) (*models.Vulnerability, bool) {
	if !entitlement.Active().Allows(cap) {
		response.WriteError(w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return nil, false
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return nil, false
	}
	v, err := h.Store.GetVulnerabilityByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return nil, false
	}
	if !vulnerabilityVisibleToCaller(w, r, h, v, claims) {
		return nil, false
	}
	attachVulnerabilityEvidence(r.Context(), h, v)
	return v, true
}

func correlator() (intelligence.Correlator, bool) {
	svc, ok := edition.Default.Service(entitlement.Intelligence)
	if !ok {
		return nil, false
	}
	c, ok := svc.(intelligence.Correlator)
	return c, ok
}

func verifyEngine() (verification.Engine, bool) {
	svc, ok := edition.Default.Service(entitlement.Verification)
	if !ok {
		return nil, false
	}
	e, ok := svc.(verification.Engine)
	return e, ok
}
