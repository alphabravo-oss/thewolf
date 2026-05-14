package routes

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
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
	sortField := q.Get("sort")
	sortOrder := q.Get("order")
	page := parseIntDefault(q.Get("page"), 1)
	perPage := parseIntDefault(q.Get("per_page"), 50)

	if sortField == "" {
		sortField = "composite_score"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}
	if page < 1 {
		page = 1
	}

	// Collect findings from user's scans.
	findings, err := gatherUserFindings(ctx, h, claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
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
		severities:       severityFilter,
		categories:       categoryFilter,
		tools:            toolFilter,
		repoID:           repoIDFilter,
		statuses:         statusFilter,
		collectionRepoIDs: collectionRepoIDs,
	})

	// Sort.
	sortFindings(filtered, sortField, sortOrder)

	// Paginate.
	total := len(filtered)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	pageData := filtered[start:end]

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
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("finding %s not found", id))
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: finding})
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
	_, err := h.Store.GetFindingByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("finding %s not found", id))
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

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: updated})
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
	Date   string              `json:"date"`
	Counts trendSeverityCounts `json:"counts"`
}

// computeTrends builds trend data from the user's scans and findings.
func computeTrends(ctx context.Context, h *Handler, userID, collectionID string) ([]trendEntry, error) {
	scans, err := h.Store.ListScansByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	if collectionID != "" {
		filtered := scans[:0]
		for _, s := range scans {
			if s.CollectionID != nil && *s.CollectionID == collectionID {
				filtered = append(filtered, s)
			}
		}
		scans = filtered
	}

	scanDateMap := make(map[string]string, len(scans))
	for _, s := range scans {
		scanDateMap[s.ID] = s.CreatedAt.Format("2006-01-02")
	}

	var allFindings []models.Finding
	for _, s := range scans {
		findings, err := h.Store.ListFindingsByScan(ctx, s.ID)
		if err != nil {
			continue
		}
		allFindings = append(allFindings, findings...)
	}

	grouped := make(map[string]*trendSeverityCounts)
	for _, f := range allFindings {
		date := scanDateMap[f.ScanID]
		if date == "" {
			date = f.CreatedAt.Format("2006-01-02")
		}

		counts, ok := grouped[date]
		if !ok {
			counts = &trendSeverityCounts{}
			grouped[date] = counts
		}

		counts.Total++
		switch f.Severity {
		case models.SeverityCritical:
			counts.Critical++
		case models.SeverityHigh:
			counts.High++
		case models.SeverityMedium:
			counts.Medium++
		case models.SeverityLow:
			counts.Low++
		case models.SeverityInfo:
			counts.Info++
		}
	}

	trends := make([]trendEntry, 0, len(grouped))
	for date, counts := range grouped {
		trends = append(trends, trendEntry{Date: date, Counts: *counts})
	}
	sort.Slice(trends, func(i, j int) bool {
		return trends[i].Date < trends[j].Date
	})
	return trends, nil
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
		w.WriteHeader(http.StatusOK)
		// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
		// nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
		w.Write(data)

	default: // csv
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="findings-trends.csv"`)
		w.WriteHeader(http.StatusOK)

		cw := csv.NewWriter(w)
		cw.Write([]string{"date", "critical", "high", "medium", "low", "info", "total"})
		for _, t := range trends {
			cw.Write([]string{
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
		if _, serr := h.Store.GetScanByID(ctx, scanID); serr != nil {
			response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
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

	// Apply filters.
	severityFilter := parseCSV(q.Get("severity"))
	categoryFilter := parseCSV(q.Get("category"))
	toolFilter := parseCSV(q.Get("tool"))
	repoIDFilter := q.Get("repo_id")
	statusFilter := parseCSV(q.Get("status"))
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
		collectionRepoIDs: collectionRepoIDs,
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
		w.WriteHeader(http.StatusOK)
		// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
		// nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
		w.Write(data)

	default: // csv
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="findings.csv"`)
		w.WriteHeader(http.StatusOK)

		cw := csv.NewWriter(w)
		cw.Write([]string{
			"id", "severity", "category", "tool_name", "title", "file_path",
			"line_start", "line_end", "status", "composite_score", "rule_id", "cwe_id", "created_at",
		})
		for _, f := range filtered {
			cw.Write([]string{
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type updateStatusRequest struct {
	Status string `json:"status"`
}

type findingFilter struct {
	severities        []string
	categories        []string
	tools             []string
	repoID            string
	statuses          []string
	collectionRepoIDs map[string]bool
}

// gatherUserFindings loads all findings across the user's scans.
func gatherUserFindings(ctx context.Context, h *Handler, userID string) ([]models.Finding, error) {
	scans, err := h.Store.ListScansByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	var all []models.Finding
	for _, s := range scans {
		findings, err := h.Store.ListFindingsByScan(ctx, s.ID)
		if err != nil {
			continue
		}
		all = append(all, findings...)
	}
	return all, nil
}

// filterFindings returns findings matching all specified filter criteria.
func filterFindings(findings []models.Finding, f findingFilter) []models.Finding {
	result := make([]models.Finding, 0, len(findings))
	for _, finding := range findings {
		if len(f.severities) > 0 && !containsStr(f.severities, string(finding.Severity)) {
			continue
		}
		if len(f.categories) > 0 && !containsStr(f.categories, string(finding.Category)) {
			continue
		}
		if len(f.tools) > 0 && !containsStr(f.tools, finding.ToolName) {
			continue
		}
		if f.repoID != "" && finding.RepoID != f.repoID {
			continue
		}
		if len(f.statuses) > 0 && !containsStr(f.statuses, string(finding.Status)) {
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
		var less bool
		switch field {
		case "composite_score":
			less = findings[i].CompositeScore < findings[j].CompositeScore
		case "severity":
			less = severityRank(findings[i].Severity) < severityRank(findings[j].Severity)
		case "created_at":
			less = findings[i].CreatedAt.Before(findings[j].CreatedAt)
		case "tool_name":
			less = findings[i].ToolName < findings[j].ToolName
		case "title":
			less = findings[i].Title < findings[j].Title
		default:
			less = findings[i].CompositeScore < findings[j].CompositeScore
		}
		if desc {
			return !less
		}
		return less
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
