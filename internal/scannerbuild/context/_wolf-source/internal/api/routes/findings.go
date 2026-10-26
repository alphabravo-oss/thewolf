package routes

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// ListFindings handles GET /api/findings — aggregated findings across all user scans.
// Query params: severity, category, tool, repo_id, collection_id, status, sort, order, page, per_page
func ListFindings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	ctx := r.Context()

	// Parse query parameters.
	q := r.URL.Query()
	severityFilter := parseCSV(q.Get("severity"))
	categoryFilter := parseCSV(q.Get("category"))
	toolFilter := parseCSV(q.Get("tool"))
	repoIDFilter := q.Get("repo_id")
	collectionID := q.Get("collection_id")
	statusFilter := parseCSV(q.Get("status"))
	baselineStateFilter := parseCSV(q.Get("baseline_state"))
	sortField := q.Get("sort")
	sortOrder := q.Get("order")
	page, perPage := parsePagination(r)
	if sortField == "" {
		sortField = "composite_score"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	result, err := h.Store.QueryCurrentFindings(ctx, claims.UserID, fleetVisible(ctx, h.Store, claims.UserID), collectionID, repoIDFilter, db.FindingListQuery{
		Severities: severityFilter, Categories: categoryFilter, Tools: toolFilter, Statuses: statusFilter, BaselineStates: baselineStateFilter,
		Search: q.Get("q"), SortBy: sortField, Desc: strings.EqualFold(sortOrder, "desc"),
		IncludeSuppressed: q.Get("include_suppressed") == "true", IncludeSARIF: q.Get("include_sarif") == "true", Limit: perPage, Offset: (page - 1) * perPage,
	})
	if err == nil {
		response.WriteJSON(w, http.StatusOK, response.ListResponse{Data: result.Findings, Meta: response.ListMeta{Total: result.Total, Page: page, PerPage: perPage}})
		return
	}
	if !errors.Is(err, db.ErrLiveFindingPolicy) {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
	}
	// Uncaptured live scans must apply filesystem policy before page boundaries.
	findings, err := gatherUserFindings(ctx, h, claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
	}
	if err := applyPathSuppressionsForFindings(ctx, h, findings); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}
	if q.Get("include_suppressed") != "true" {
		findings = dropSuppressed(findings)
	}

	// If collection_id is specified, resolve the repo IDs in that collection.
	var collectionRepoIDs map[string]bool
	if collectionID != "" {
		repos, err := h.Store.ListReposInCollection(ctx, collectionID)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load collection repos")
			return
		}
		collectionRepoIDs = make(map[string]bool, len(repos))
		for _, repo := range repos {
			collectionRepoIDs[repo.ID] = true
		}
	}

	// Filter.
	filtered := filterFindings(findings, findingFilter{
		severities:        severityFilter,
		categories:        categoryFilter,
		tools:             toolFilter,
		repoID:            repoIDFilter,
		statuses:          statusFilter,
		baselineStates:    baselineStateFilter,
		collectionRepoIDs: collectionRepoIDs,
		search:            q.Get("q"),
	})

	// Sort.
	sortFindings(filtered, sortField, sortOrder)

	// Paginate.
	total := len(filtered)
	start, end := paginateSlice(total, page, perPage)
	pageData := filtered[start:end]
	if q.Get("include_sarif") != "true" {
		for i := range pageData {
			pageData[i].SARIFData = ""
		}
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: pageData,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetFinding handles GET /api/findings/:id — returns a single finding.
func GetFinding(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	finding, err := h.Store.GetFindingByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "finding not found")
		return
	}
	if !findingVisibleToCaller(w, r, h.Store, finding, claims) {
		return
	}

	type strategyBody struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	out := struct {
		models.Finding
		FixStrategy *strategyBody    `json:"fix_strategy,omitempty"`
		Evidence    []models.Finding `json:"evidence,omitempty"`
	}{Finding: *finding}
	if finding.ClusterID != "" {
		evidence, err := h.Store.ListFindingClusterEvidence(r.Context(), finding.ID)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load finding evidence")
			return
		}
		out.Evidence = evidence
	}
	projected := append([]models.Finding{*finding}, out.Evidence...)
	if err := applyPathSuppressions(r.Context(), h, finding.RepoID, projected); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}
	out.Finding, out.Evidence = projected[0], projected[1:]
	if finding.FixStrategyID != "" {
		if s, ok := knowledge.GetStrategy(finding.FixStrategyID); ok {
			out.FixStrategy = &strategyBody{ID: s.ID, Title: s.Title, Body: s.Body}
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
}

// UpdateFindingStatus handles PUT /api/findings/:id/status — update finding status.
func UpdateFindingStatus(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")

	// Verify finding exists.
	finding, err := h.Store.GetFindingByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "finding not found")
		return
	}
	if !findingVisibleToCaller(w, r, h.Store, finding, claims) {
		return
	}
	// Parse request body.
	var req updateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Validate status value.
	newStatus := models.Status(req.Status)
	if !isValidFindingStatus(newStatus) {
		response.WriteError(w, http.StatusBadRequest, "validation_error",
			fmt.Sprintf("invalid status %q; allowed values: open, wont_fix, false_positive", req.Status))
		return
	}

	// Validate the response projection before committing triage.
	if err := applyPathSuppressions(r.Context(), h, finding.RepoID, []models.Finding{*finding}); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}

	if err := h.Store.UpdateFindingStatus(r.Context(), id, newStatus); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update finding status")
		return
	}

	// Return updated finding.
	updated, err := h.Store.GetFindingByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to retrieve updated finding")
		return
	}

	visible := []models.Finding{*updated}
	if err := applyPathSuppressions(r.Context(), h, updated.RepoID, visible); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: visible[0]})
}

// trendSeverityCounts holds per-date severity counts for trend data.
type trendSeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
	Total    int `json:"total"`
}

// trendEntry represents a single row of trend data.
type trendEntry struct {
	Date            string              `json:"date"`
	Counts          trendSeverityCounts `json:"counts"`
	Repos           int                 `json:"repos"`
	IncompleteScans int                 `json:"incomplete_scans"`
}

// computeTrends returns daily snapshots of distinct observed open locations.
// Missing scans carry forward; partial scans cannot claim a reduction.
func computeTrends(ctx context.Context, h *Handler, userID, collectionID string) ([]trendEntry, error) {
	rows, err := h.Store.FleetFindingTrends(ctx, userID, fleetVisible(ctx, h.Store, userID), collectionID)
	if err != nil {
		return nil, err
	}
	out := make([]trendEntry, 0, len(rows))
	for _, row := range rows {
		c := row.Counts
		out = append(out, trendEntry{Date: row.Date, Counts: trendSeverityCounts{Critical: c["critical"], High: c["high"], Medium: c["medium"], Low: c["low"], Info: c["info"], Total: c["total"]}, Repos: row.Repos, IncompleteScans: row.IncompleteScans})
	}
	return out, nil
}

// FindingTrends handles GET /api/findings/trends — findings count grouped by date and severity.
func FindingTrends(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := r.URL.Query().Get("collection_id")
	trends, err := computeTrends(r.Context(), h, claims.UserID, collectionID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load trends")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: trends})
}

// ExportFindingTrends handles GET /api/findings/trends/export — download trend data as CSV or JSON.
func ExportFindingTrends(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := r.URL.Query().Get("collection_id")
	trends, err := computeTrends(r.Context(), h, claims.UserID, collectionID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load trends")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}

	switch strings.ToLower(format) {
	case "json":
		data, err := json.MarshalIndent(trends, "", "  ")
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to marshal JSON")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="findings-trends.json"`)
		w.WriteHeader(http.StatusOK) // #nosec G104 -- intentional: response/log write errors are not actionable here
		_, _ = w.Write(data)         // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: response/log write errors are not actionable here

	default: // csv
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="findings-trends.csv"`)
		w.WriteHeader(http.StatusOK) // #nosec G104 -- intentional: response/log write errors are not actionable here

		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"date", "critical", "high", "medium", "low", "info", "total"}) // #nosec G104 -- intentional: response/log write errors are not actionable here
		for _, t := range trends {
			_ = cw.Write([]string{
				t.Date,
				strconv.Itoa(t.Counts.Critical),
				strconv.Itoa(t.Counts.High),
				strconv.Itoa(t.Counts.Medium),
				strconv.Itoa(t.Counts.Low),
				strconv.Itoa(t.Counts.Info),
				strconv.Itoa(t.Counts.Total),
			})
		}
		cw.Flush()
	}
}

// ExportFindings handles GET /api/findings/export — download findings as CSV or JSON.
func ExportFindings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	scanID := q.Get("scan_id")
	var findings []models.Finding
	var err error

	if scanID != "" {
		if _, ok := loadScanForCaller(w, r, h.Store, scanID, claims); !ok {
			return
		}
		findings, err = h.Store.ListFindingsByScan(ctx, scanID)
	} else {
		findings, err = gatherUserFindings(ctx, h, claims.UserID)
	}
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
	}
	if err := applyPathSuppressionsForFindings(ctx, h, findings); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}
	if q.Get("include_suppressed") != "true" {
		findings = dropSuppressed(findings)
	}

	// Apply filters.
	severityFilter := parseCSV(q.Get("severity"))
	categoryFilter := parseCSV(q.Get("category"))
	toolFilter := parseCSV(q.Get("tool"))
	repoIDFilter := q.Get("repo_id")
	statusFilter := parseCSV(q.Get("status"))
	baselineStateFilter := parseCSV(q.Get("baseline_state"))
	collectionID := q.Get("collection_id")

	var collectionRepoIDs map[string]bool
	if collectionID != "" {
		repos, cerr := h.Store.ListReposInCollection(ctx, collectionID)
		if cerr != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load collection repos")
			return
		}
		collectionRepoIDs = make(map[string]bool, len(repos))
		for _, repo := range repos {
			collectionRepoIDs[repo.ID] = true
		}
	}

	filtered := filterFindings(findings, findingFilter{
		severities:        severityFilter,
		categories:        categoryFilter,
		tools:             toolFilter,
		repoID:            repoIDFilter,
		statuses:          statusFilter,
		baselineStates:    baselineStateFilter,
		collectionRepoIDs: collectionRepoIDs,
		search:            q.Get("q"),
	})

	format := q.Get("format")
	if format == "" {
		format = "csv"
	}

	switch strings.ToLower(format) {
	case "json":
		data, err := json.MarshalIndent(filtered, "", "  ")
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to marshal JSON")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="findings.json"`)
		w.WriteHeader(http.StatusOK) // #nosec G104 -- intentional: response/log write errors are not actionable here
		_, _ = w.Write(data)         // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: response/log write errors are not actionable here

	default: // csv
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="findings.csv"`)
		w.WriteHeader(http.StatusOK) // #nosec G104 -- intentional: response/log write errors are not actionable here

		cw := csv.NewWriter(w)
		_ = cw.Write([]string{
			"id", "severity", "category", "tool_name", "title", "file_path",
			"line_start", "line_end", "status", "composite_score", "rule_id", "cwe_id", "created_at",
		})
		for _, f := range filtered {
			_ = cw.Write([]string{
				f.ID,
				string(f.Severity),
				string(f.Category),
				f.ToolName,
				f.Title,
				f.FilePath,
				strconv.Itoa(f.LineStart),
				strconv.Itoa(f.LineEnd),
				string(f.Status),
				fmt.Sprintf("%.2f", f.CompositeScore),
				f.RuleID,
				f.CWEID,
				f.CreatedAt.Format(time.RFC3339),
			})
		}
		cw.Flush()
	}
}

type bulkUpdateFindingsRequest struct {
	IDs      []string `json:"ids"`
	Status   string   `json:"status,omitempty"`
	Suppress *struct {
		Reason string `json:"reason"`
	} `json:"suppress,omitempty"`
}

// BulkUpdateFindings handles POST /api/findings/bulk.
func BulkUpdateFindings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req bulkUpdateFindingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "ids is required")
		return
	}
	if len(req.IDs) > 200 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "at most 200 ids are allowed")
		return
	}

	var newStatus models.Status
	if req.Status != "" {
		newStatus = models.Status(req.Status)
		if !isValidFindingStatus(newStatus) {
			response.WriteError(w, http.StatusBadRequest, "validation_error",
				fmt.Sprintf("invalid status %q; allowed values: open, wont_fix, false_positive", req.Status))
			return
		}
	}

	findings := make([]*models.Finding, 0, len(req.IDs))
	for _, id := range req.IDs {
		finding, err := h.Store.GetFindingByID(r.Context(), id)
		if err != nil || !findingAccessibleToCaller(r, h.Store, finding, claims) {
			response.WriteError(w, http.StatusForbidden, "forbidden", "finding not visible")
			return
		}
		findings = append(findings, finding)
	}

	suppressReason := ""
	if req.Suppress != nil {
		suppressReason = strings.TrimSpace(req.Suppress.Reason)
	}

	for _, finding := range findings {
		if req.Status != "" {
			if err := h.Store.UpdateFindingStatus(r.Context(), finding.ID, newStatus); err != nil {
				response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update finding status")
				return
			}
		}
		if suppressReason != "" {
			s, ok := buildSuppressionFromRequest(w, r, h, claims.UserID, suppressionRequest{
				FindingID: finding.ID,
				Reason:    suppressReason,
			}, nil)
			if !ok {
				return
			}
			if err := h.Store.CreateFindingSuppression(r.Context(), s); err != nil {
				response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create suppression")
				return
			}

		}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]int{"updated": len(findings)}})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type updateStatusRequest struct {
	Status string `json:"status"`
}

type findingFilter struct {
	search            string
	severities        []string
	categories        []string
	tools             []string
	repoID            string
	statuses          []string
	baselineStates    []string
	collectionRepoIDs map[string]bool
}

// gatherUserFindings loads all findings across the user's scans. When
// fleet_mode is on, scope widens to every scan in the org.
func gatherUserFindings(ctx context.Context, h *Handler, userID string) ([]models.Finding, error) {
	return h.Store.ListCurrentOpenFindings(ctx, userID, fleetVisible(ctx, h.Store, userID), "")
}

// filterFindings returns findings matching all specified filter criteria.
func filterFindings(findings []models.Finding, f findingFilter) []models.Finding {
	result := make([]models.Finding, 0, len(findings))
	search := strings.ToLower(strings.TrimSpace(f.search))
	for _, finding := range findings {
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{finding.Title, finding.RuleID, finding.FilePath, finding.ToolName, finding.Description}, " ")), search) {
			continue
		}
		if len(f.severities) > 0 && !containsStr(f.severities, string(finding.Severity)) {
			continue
		}
		if len(f.categories) > 0 && !containsStr(f.categories, string(finding.Category)) {
			continue
		}
		if len(f.tools) > 0 {
			matches := false
			for _, tool := range finding.ReportingTools() {
				if containsStr(f.tools, tool) {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
		}
		if f.repoID != "" && finding.RepoID != f.repoID {
			continue
		}
		if len(f.statuses) > 0 && !containsStr(f.statuses, string(finding.Status)) {
			continue
		}
		if len(f.baselineStates) > 0 && !containsStr(f.baselineStates, finding.BaselineState) {
			continue
		}
		if f.collectionRepoIDs != nil && !f.collectionRepoIDs[finding.RepoID] {
			continue
		}
		result = append(result, finding)
	}
	return result
}

// sortFindings sorts findings in place by the given field and order.
func sortFindings(findings []models.Finding, field, order string) {
	desc := strings.EqualFold(order, "desc")

	sort.SliceStable(findings, func(i, j int) bool {
		var comparison int
		switch field {
		case "severity":
			comparison = cmp.Compare(severityRank(findings[i].Severity), severityRank(findings[j].Severity))
		case "created_at":
			comparison = findings[i].CreatedAt.Compare(findings[j].CreatedAt)
		case "tool_name":
			comparison = strings.Compare(findings[i].ToolName, findings[j].ToolName)
		case "title":
			comparison = strings.Compare(findings[i].Title, findings[j].Title)
		case "file_path":
			comparison = strings.Compare(findings[i].FilePath, findings[j].FilePath)
		case "line_start":
			comparison = cmp.Compare(findings[i].LineStart, findings[j].LineStart)
		default:
			comparison = cmp.Compare(findings[i].CompositeScore, findings[j].CompositeScore)
		}
		if comparison == 0 {
			return findings[i].ID < findings[j].ID
		}
		if desc {
			return comparison > 0
		}
		return comparison < 0
	})
}

// severityRank is defined in scans.go — shared by both scans and findings routes.

// isValidFindingStatus checks if the given status is allowed for manual updates.
func isValidFindingStatus(s models.Status) bool {
	switch s {
	case models.StatusOpen, models.StatusWontFix, models.StatusFalsePositive:
		return true
	default:
		return false
	}
}

// parseCSV splits a comma-separated string into a slice, trimming whitespace.
// Returns nil for empty input.
func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// parseIntDefault parses an integer with a default fallback.
func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// containsStr checks if a string slice contains a value (case-insensitive).
func containsStr(slice []string, val string) bool {
	val = strings.ToLower(val)
	for _, s := range slice {
		if strings.ToLower(s) == val {
			return true
		}
	}
	return false
}
