package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type policyRequest struct {
	Name      string       `json:"name"`
	Scope     string       `json:"scope,omitempty"`
	ScopeID   string       `json:"scope_id,omitempty"`
	Mode      string       `json:"mode,omitempty"`
	Rules     []gates.Rule `json:"rules,omitempty"`
	RulesJSON string       `json:"rules_json,omitempty"`
	Enabled   *bool        `json:"enabled,omitempty"`
}

type gateResponse struct {
	Result       models.QualityGateResult `json:"result"`
	Evaluation   gates.Evaluation         `json:"evaluation"`
	Policy       models.QualityPolicy     `json:"policy"`
	Persisted    bool                     `json:"persisted"`
	FindingCount int                      `json:"finding_count"`
}

func GetScanGate(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	scanID := chi.URLParam(r, "id")
	result, eval, policy, count, err := evaluateAndPersistGateContext(r.Context(), h, scanID, claims.UserID)
	if err != nil {
		var forbidden forbiddenError
		if errors.As(err, &forbidden) {
			response.WriteError(w, http.StatusForbidden, "forbidden", "scan does not belong to current user")
			return
		}
		response.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if scan, scanErr := h.Store.GetScanByID(r.Context(), scanID); scanErr == nil {
		_ = writeGateResultArtifact(r.Context(), h, scan, result, eval, policy, count, artifactDirForScan(r.Context(), h, scan))
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: gateResponse{Result: result, Evaluation: eval, Policy: policy, Persisted: true, FindingCount: count},
	})
}

func ListPolicies(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	policies, err := h.Store.ListQualityPolicies(r.Context(), r.URL.Query().Get("scope"), r.URL.Query().Get("scope_id"))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list policies")
		return
	}
	if !claims.IsAdmin() {
		filtered := policies[:0]
		for _, policy := range policies {
			if policy.Scope != "repo" {
				continue
			}
			if repo, err := h.Store.GetRepoByID(r.Context(), policy.ScopeID); err == nil && repo.UserID == claims.UserID {
				filtered = append(filtered, policy)
			}
		}
		policies = filtered
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: policies,
		Meta: response.ListMeta{Total: len(policies), Page: 1, PerPage: len(policies)},
	})
}

func CreatePolicy(w http.ResponseWriter, r *http.Request) {
	upsertPolicy(w, r, "")
}

func UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	upsertPolicy(w, r, chi.URLParam(r, "id"))
}

func upsertPolicy(w http.ResponseWriter, r *http.Request, id string) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	var req policyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	scope := defaultString(req.Scope, "global")
	if scope != "global" && scope != "repo" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "scope must be global or repo")
		return
	}
	if scope == "global" && !claims.IsAdmin() {
		response.WriteError(w, http.StatusForbidden, "forbidden", "global policies require an administrator")
		return
	}
	if scope == "repo" {
		if req.ScopeID == "" {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "scope_id is required for repo policies")
			return
		}
		if _, ok := loadRepoForCaller(w, r, h.Store, req.ScopeID, claims); !ok {
			return
		}
	}
	rulesJSON := req.RulesJSON
	if len(req.Rules) > 0 {
		data, err := json.Marshal(req.Rules)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid policy rules")
			return
		}
		rulesJSON = string(data)
	}
	if _, err := gates.ParsePolicy(req.Name, req.Mode, rulesJSON); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "rules_json is invalid")
		return
	}
	if id == "" {
		id = uuid.New().String()
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	policy := &models.QualityPolicy{
		ID:        id,
		Name:      req.Name,
		Scope:     scope,
		ScopeID:   req.ScopeID,
		Mode:      defaultString(req.Mode, "warn"),
		RulesJSON: defaultString(rulesJSON, "[]"),
		Enabled:   enabled,
		CreatedBy: claims.UserID,
	}
	if err := h.Store.UpsertQualityPolicy(r.Context(), policy); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to save policy")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: policy})
}

func evaluateAndPersistGateContext(ctx context.Context, h *Handler, scanID, userID string) (models.QualityGateResult, gates.Evaluation, models.QualityPolicy, int, error) {
	scan, err := h.Store.GetScanByID(ctx, scanID)
	if err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	if scan.UserID != userID {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, errForbidden()
	}
	findings, err := h.Store.ListFindingsByScan(ctx, scanID)
	if err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	policy := resolvePolicy(ctx, h, scan)
	var parsed gates.Policy
	if policy.ID == "default" && isFastProfile(scan.Profile) {
		parsed = gates.FastPRPolicy()
		policy.Name = parsed.Name
		policy.Mode = parsed.Mode
		if data, merr := json.Marshal(parsed.Rules); merr == nil {
			policy.RulesJSON = string(data)
		}
	} else {
		parsed, err = gates.ParsePolicy(policy.Name, policy.Mode, policy.RulesJSON)
		if err != nil {
			parsed = gates.DefaultPolicy()
		}
	}
	applyPathSuppressions(ctx, h, scan.RepoID, findings)
	eval := gates.Evaluate(parsed, findings)
	summaryJSON, _ := json.Marshal(eval.Summary)
	matchesJSON, _ := json.Marshal(eval.MatchedRules)
	now := time.Now().UTC()
	result := models.QualityGateResult{
		ID:               uuid.New().String(),
		ScanID:           scanID,
		PolicyID:         policy.ID,
		Status:           eval.Status,
		SummaryJSON:      string(summaryJSON),
		MatchedRulesJSON: string(matchesJSON),
		EvaluatedAt:      now,
	}
	if err := h.Store.UpsertQualityGateResult(ctx, &result); err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	return result, eval, policy, len(findings), nil
}

func resolvePolicy(ctx context.Context, h *Handler, scan *models.Scan) models.QualityPolicy {
	if policies, err := h.Store.ListQualityPolicies(ctx, "repo", scan.RepoID); err == nil {
		for _, policy := range policies {
			if policy.Enabled {
				return policy
			}
		}
	}
	if policies, err := h.Store.ListQualityPolicies(ctx, "global", ""); err == nil {
		for _, policy := range policies {
			if policy.Enabled {
				return policy
			}
		}
	}
	defaultPolicy := gates.DefaultPolicy()
	rulesJSON, _ := json.Marshal(defaultPolicy.Rules)
	return models.QualityPolicy{
		ID:        "default",
		Name:      defaultPolicy.Name,
		Scope:     "global",
		Mode:      defaultPolicy.Mode,
		RulesJSON: string(rulesJSON),
		Enabled:   true,
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeGateResultArtifact(ctx context.Context, h *Handler, scan *models.Scan, result models.QualityGateResult, eval gates.Evaluation, policy models.QualityPolicy, findingCount int, dir string) error {
	if scan == nil {
		return nil
	}
	payload := gateResponse{
		Result:       result,
		Evaluation:   eval,
		Policy:       policy,
		Persisted:    true,
		FindingCount: findingCount,
	}
	_, err := writeJSONScanArtifact(ctx, h, scan.ID, dir, "gate-result.json", payload)
	return err
}

type forbiddenError struct{}

func (forbiddenError) Error() string { return "forbidden" }

func errForbidden() error { return forbiddenError{} }
