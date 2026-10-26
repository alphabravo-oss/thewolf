package routes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
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
	result, eval, policy, count, err := evaluateAndPersistGateContext(r.Context(), h, scanID, claims)
	if err != nil {
		if errors.Is(err, db.ErrStaleFindings) {
			response.WriteError(w, http.StatusConflict, "findings_changed", err.Error())
			return
		}
		var forbidden forbiddenError
		if errors.As(err, &forbidden) {
			response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
		} else {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to evaluate quality gate")
		}
		return
	}
	if err := writeGateResultArtifact(r.Context(), h, &models.Scan{ID: scanID}, result, eval, policy, count); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "artifact_error", "failed to publish gate artifact")
		return
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
			if policy.Scope == "collection" {
				if collection, err := h.Store.GetCollectionByID(r.Context(), policy.ScopeID); err == nil && collection.UserID == claims.UserID {
					filtered = append(filtered, policy)
				}
				continue
			}
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
	if scope != "global" && scope != "repo" && scope != "collection" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "scope must be global, collection or repo")
		return
	}
	if id != "" {
		previous, err := h.Store.GetQualityPolicyByID(r.Context(), id)
		if err != nil {
			response.WriteError(w, http.StatusNotFound, "not_found", "policy not found")
			return
		}
		if !policyScopeWritable(w, r, h, previous.Scope, previous.ScopeID, claims) {
			return
		}
		// The existing upsert identifies a policy by scope/name. Keep identity
		// stable when updating by ID; create another policy to change its scope.
		if previous.Scope != scope || previous.ScopeID != req.ScopeID || previous.Name != req.Name {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "policy scope and name cannot change on update")
			return
		}
	}
	if !policyScopeWritable(w, r, h, scope, req.ScopeID, claims) {
		return
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
		response.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
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

func evaluateAndPersistGateContext(ctx context.Context, h *Handler, scanID string, claims *auth.Claims) (models.QualityGateResult, gates.Evaluation, models.QualityPolicy, int, error) {
	scan, findings, err := h.Store.GetScanFindingSnapshot(ctx, scanID)
	if err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	if !canModifyOwned(claims, scan.UserID) {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, errForbidden()
	}
	if err := applyPathSuppressions(ctx, h, scan.RepoID, findings); err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	return evaluateAndPersistGateSnapshot(ctx, h, scan, findings)
}

func evaluateAndPersistGateSnapshot(ctx context.Context, h *Handler, scan *models.Scan, findings []models.Finding) (models.QualityGateResult, gates.Evaluation, models.QualityPolicy, int, error) {
	result, eval, policy, count, err := evaluateGateSnapshot(ctx, h, scan, findings)
	if err == nil {
		err = h.Store.UpsertQualityGateResult(ctx, &result)
	}
	return result, eval, policy, count, err
}

// Callers supply one already-projected finding snapshot, shared with their totals
// or report bundle. Re-reading mutable rules here would mix policy versions.
func evaluateGateSnapshot(ctx context.Context, h *Handler, scan *models.Scan, findings []models.Finding) (models.QualityGateResult, gates.Evaluation, models.QualityPolicy, int, error) {
	policy, err := resolvePolicy(ctx, h, scan)
	if err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
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
			return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
		}
	}
	eval := gates.EvaluateScan(parsed, scan, findings)
	if err := coverageForGate(ctx, h, scan, parsed, &eval); err != nil {
		return models.QualityGateResult{}, gates.Evaluation{}, models.QualityPolicy{}, 0, err
	}
	summaryJSON, _ := json.Marshal(eval.Summary)
	matchesJSON, _ := json.Marshal(eval.MatchedRules)
	now := time.Now().UTC()
	result := models.QualityGateResult{
		ScanStatus:       scan.Status,
		ID:               uuid.New().String(),
		ScanID:           scan.ID,
		FindingsRevision: scan.FindingsRevision,
		PolicyID:         policy.ID,
		Status:           eval.Status,
		SummaryJSON:      string(summaryJSON),
		MatchedRulesJSON: string(matchesJSON),
		EvaluatedAt:      now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	visible := 0
	for _, finding := range findings {
		if !finding.Suppressed {
			visible++
		}
	}
	return result, eval, policy, visible, nil
}

func resolvePolicy(ctx context.Context, h *Handler, scan *models.Scan) (models.QualityPolicy, error) {
	scopes := [][2]string{{"repo", scan.RepoID}}
	if scan.CollectionID != nil {
		scopes = append(scopes, [2]string{"collection", *scan.CollectionID})
	}
	scopes = append(scopes, [2]string{"global", ""})
	for _, scope := range scopes {
		policies, err := h.Store.ListQualityPolicies(ctx, scope[0], scope[1])
		if err != nil {
			return models.QualityPolicy{}, err
		}
		for _, policy := range policies {
			if policy.Enabled {
				return policy, nil
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
	}, nil
}

func policyScopeWritable(w http.ResponseWriter, r *http.Request, h *Handler, scope, scopeID string, claims *auth.Claims) bool {
	switch scope {
	case "global":
		if !claims.IsAdmin() {
			response.WriteError(w, 403, "forbidden", "global policies require an administrator")
			return false
		}
		if scopeID != "" {
			response.WriteError(w, 400, "bad_request", "global policies must have an empty scope_id")
			return false
		}
	case "repo":
		_, ok := loadRepoForCaller(w, r, h.Store, scopeID, claims)
		return ok
	case "collection":
		collection, err := h.Store.GetCollectionByID(r.Context(), scopeID)
		if err != nil {
			response.WriteError(w, 404, "not_found", "collection not found")
			return false
		}
		if !canModifyOwned(claims, collection.UserID) {
			response.WriteError(w, 403, "forbidden", "collection does not belong to current user")
			return false
		}
	}
	return true
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeGateResultArtifact(ctx context.Context, h *Handler, scan *models.Scan, result models.QualityGateResult, eval gates.Evaluation, policy models.QualityPolicy, findingCount int) error {
	artifact, err := prepareGateResultArtifact(ctx, scan, result, eval, policy, findingCount)
	if err != nil {
		return err
	}
	return h.Store.CreateScanArtifact(ctx, artifact)
}

// Publish immutable bytes first. Completion exposes their reference only when
// the terminal status and evaluation commit; failed attempts leave no success link.
func prepareGateResultArtifact(ctx context.Context, scan *models.Scan, result models.QualityGateResult, eval gates.Evaluation, policy models.QualityPolicy, findingCount int) (*models.ScanArtifact, error) {
	if scan == nil {
		return nil, errors.New("gate artifact requires a scan")
	}
	payload := gateResponse{Result: result, Evaluation: eval, Policy: policy, Persisted: true, FindingCount: findingCount}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	artifact, err := artifacts.Publish(ctx, scan.ID, "gate-result.json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	artifact.ArtifactType = models.ArtifactJSON
	artifact.RedactionLevel = artifactRedactionLevel(models.ArtifactJSON, "gate-result.json")
	artifact.FindingsRevision = &result.FindingsRevision
	return artifact, nil
}

var errScanArtifactPublication = errors.New("scan artifact publication failed")

// Retry the publication boundary, never the scanners or their persisted evidence.
// ponytail: three fresh snapshots bound work under continuous triage; exhaustion
// remains an explicit conflict rather than unbounded report/file generation.
// Measure sustained contention before adding durable finalization retries.
func publishScanResults(ctx context.Context, h *Handler, scan *models.Scan, dir string, rcfg report.ReportConfig, manifest report.Manifest, completion db.ScanCompletion) (string, []models.Finding, error) {
	for range 3 {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		snapshot, findings, err := h.Store.GetScanFindingSnapshot(ctx, scan.ID)
		if err != nil {
			return "", nil, err
		}
		if snapshot.Status != models.ScanStatusRunning || snapshot.LeaseToken != scan.LeaseToken || snapshot.CancelRequestedAt != nil {
			return "", nil, db.ErrStaleScanLease
		}
		if snapshot.IgnorePolicyJSON != "" {
			if _, _, err := suppress.ApplyScan(ctx, nil, snapshot, ""); err != nil {
				return "", nil, err
			}
		}
		scan.IgnorePolicyJSON = snapshot.IgnorePolicyJSON
		if snapshot.IgnorePolicyJSON != "" {
			manifest.IgnorePolicy = &suppress.Snapshot{}
			if err := json.Unmarshal([]byte(snapshot.IgnorePolicyJSON), manifest.IgnorePolicy); err != nil {
				return "", nil, err
			}
		}
		scan.BaselineScanID, scan.BaselineResolvedAt, scan.FindingsRevision = snapshot.BaselineScanID, snapshot.BaselineResolvedAt, snapshot.FindingsRevision
		rcfg.ScanID, rcfg.Findings, rcfg.FindingsRevision, rcfg.BaselineScanID = scan.ID, findings, snapshot.FindingsRevision, snapshot.BaselineScanID
		manifest.ScanID, manifest.FindingsRevision, manifest.BaselineScanID = scan.ID, &snapshot.FindingsRevision, snapshot.BaselineScanID
		bundle, err := report.WriteAll(ctx, dir, rcfg, manifest)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errScanArtifactPublication, err)
		}
		if err := recordScanBundle(ctx, h, scan.ID, bundle, &snapshot.FindingsRevision); err != nil {
			return "", nil, fmt.Errorf("%w: %w", errScanArtifactPublication, err)
		}
		scan.FindingCount = report.CountFindings(0, findings).Visible
		if scan.Status != models.ScanStatusCompleted {
			return "", findings, nil
		}
		result, eval, policy, count, err := evaluateGateSnapshot(ctx, h, scan, findings)
		if err != nil {
			return "", nil, fmt.Errorf("evaluate final gate: %w", err)
		}
		artifact, err := prepareGateResultArtifact(ctx, scan, result, eval, policy, count)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errScanArtifactPublication, err)
		}
		scan.FindingCount = count
		completion.Gate, completion.GateArtifact = &result, artifact
		ok, err := h.Store.CompleteScan(ctx, scan, completion)
		if errors.Is(err, db.ErrStaleFindings) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("commit scan completion: %w", err)
		}
		if !ok {
			return "", nil, db.ErrStaleScanLease
		}
		return eval.Status, findings, nil
	}
	return "", nil, db.ErrStaleFindings
}

type forbiddenError struct{}

func (forbiddenError) Error() string { return "forbidden" }

func errForbidden() error { return forbiddenError{} }
