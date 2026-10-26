package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"github.com/go-chi/chi/v5"
)

const MaxTestCoverageRequestBytes = 40 << 20

type testCoverageRequest struct {
	CommitSHA     string                 `json:"commit_sha"`
	Reports       []coverage.ReportInput `json:"reports"`
	ExcludedPaths []string               `json:"excluded_paths,omitempty"`
	BaseCommitSHA string                 `json:"base_commit_sha,omitempty"`
	DiffCover     json.RawMessage        `json:"diff_cover,omitempty"`
}

var fullCommitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func readTestCoverage(ctx context.Context, h *Handler, scanID string) (*coverage.MeasuredReport, error) {
	stored, err := h.Store.GetTestCoverage(ctx, scanID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report coverage.MeasuredReport
	if err := json.Unmarshal([]byte(stored.ReportJSON), &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func testCoverageScan(w http.ResponseWriter, r *http.Request) (*Handler, *models.Scan, bool) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, 500, "server_error", "handler not initialized")
		return nil, nil, false
	}
	scan, ok := loadScanForCaller(w, r, h.Store, chi.URLParam(r, "id"), auth.GetUserFromContext(r.Context()))
	return h, scan, ok
}

func ImportTestCoverage(w http.ResponseWriter, r *http.Request) {
	h, scan, ok := testCoverageScan(w, r)
	if !ok {
		return
	}
	var req testCoverageRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxTestCoverageRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.WriteError(w, 413, "payload_too_large", "coverage request exceeds 40 MiB")
		} else {
			response.WriteError(w, 400, "bad_request", "invalid coverage JSON: "+err.Error())
		}
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		response.WriteError(w, 400, "bad_request", "provide one JSON object")
		return
	}
	if !fullCommitSHA.MatchString(req.CommitSHA) || req.CommitSHA != scan.CommitSHA {
		response.WriteError(w, 409, "commit_mismatch", "commit_sha must be the full commit SHA resolved by this scan")
		return
	}
	if scan.DirtyState != "" && scan.DirtyState != "clean" {
		response.WriteError(w, 409, "dirty_source", "commit coverage cannot be attached to a modified source tree")
		return
	}
	if len(req.ExcludedPaths) > 200 {
		response.WriteError(w, 400, "bad_request", "too many excluded paths")
		return
	}
	for _, p := range req.ExcludedPaths {
		if p == "" || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") {
			response.WriteError(w, 400, "bad_request", "invalid excluded path")
			return
		}
	}
	report, err := coverage.ParseReports(req.Reports)
	if err != nil {
		response.WriteError(w, 400, "invalid_coverage", err.Error())
		return
	}
	report.CommitSHA, report.ImportedAt, report.ImportedBy = req.CommitSHA, time.Now().UTC(), auth.GetUserFromContext(r.Context()).UserID
	if len(req.DiffCover) > 0 || req.BaseCommitSHA != "" {
		if !fullCommitSHA.MatchString(req.BaseCommitSHA) || len(req.DiffCover) == 0 {
			response.WriteError(w, 400, "bad_request", "diff_cover requires a full base_commit_sha and diff-cover JSON")
			return
		}
		report.Changed, err = coverage.ReadDiffCover(req.DiffCover, req.BaseCommitSHA, report)
		if err != nil {
			response.WriteError(w, 400, "invalid_coverage", err.Error())
			return
		}
	}
	if req.ExcludedPaths != nil {
		report.ExcludedPaths = req.ExcludedPaths
	}
	data, err := json.Marshal(report)
	if err != nil {
		response.WriteError(w, 500, "server_error", "cannot encode coverage")
		return
	}
	err = h.Store.SaveTestCoverage(r.Context(), &models.TestCoverage{ScanID: scan.ID, CommitSHA: req.CommitSHA, ReportJSON: string(data), ImportedBy: report.ImportedBy, ImportedAt: report.ImportedAt})
	if errors.Is(err, sql.ErrNoRows) {
		response.WriteError(w, 409, "commit_mismatch", "scan source changed during upload")
		return
	}
	if err != nil {
		response.WriteError(w, 500, "server_error", "cannot save coverage")
		return
	}
	// Re-evaluate the stored CI result immediately after replacing its evidence.
	if _, _, _, _, err := evaluateAndPersistGateContext(r.Context(), h, scan.ID, auth.GetUserFromContext(r.Context())); err != nil {
		response.WriteError(w, 500, "gate_error", "coverage saved, but gate evaluation failed; retry the gate endpoint")
		return
	}
	writeTestCoverage(w, r, h, scan, report)
}

func GetTestCoverage(w http.ResponseWriter, r *http.Request) {
	h, scan, ok := testCoverageScan(w, r)
	if !ok {
		return
	}
	report, err := readTestCoverage(r.Context(), h, scan.ID)
	if err != nil {
		response.WriteError(w, 500, "server_error", "cannot load measured coverage")
		return
	}
	writeTestCoverage(w, r, h, scan, report)
}

func writeTestCoverage(w http.ResponseWriter, r *http.Request, h *Handler, scan *models.Scan, report *coverage.MeasuredReport) {
	policy, err := resolvePolicy(r.Context(), h, scan)
	if err != nil {
		response.WriteError(w, 500, "server_error", "cannot load coverage policy")
		return
	}
	parsed, err := gates.ParsePolicy(policy.Name, policy.Mode, policy.RulesJSON)
	if err != nil {
		response.WriteError(w, 500, "invalid_policy", "cannot read coverage policy")
		return
	}
	eval := gates.Evaluation{Status: gates.StatusPass, Summary: gates.Summary{Status: gates.StatusPass}}
	commit := scan.CommitSHA
	if scan.DirtyState != "" && scan.DirtyState != "clean" {
		commit = ""
	}
	gates.ApplyCoverage(&eval, parsed, report, commit)
	status, count := "not_measured", 0
	var summary *coverage.MeasuredReport
	if report != nil {
		status, count = "measured", len(report.Files)
		if commit == "" || report.CommitSHA != commit {
			status = "source_mismatch"
		}
		copy := *report
		copy.Files = nil
		if report.Changed != nil {
			changed := *report.Changed
			changed.Files = nil
			copy.Changed = &changed
		}
		summary = &copy
	}
	response.WriteJSON(w, 200, response.SuccessResponse{Data: map[string]any{
		"status": status, "scan_id": scan.ID, "repo_id": scan.RepoID, "commit_sha": scan.CommitSHA, "file_count": count,
		"report": summary, "formats": coverage.Formats, "policy": policy, "checks": eval.Coverage,
		"scope_note": "Coverage measures the source files included by your test tooling. Include untested files and review exclusions in CI.",
	}})
}

func ListTestCoverageFiles(w http.ResponseWriter, r *http.Request) {
	h, scan, ok := testCoverageScan(w, r)
	if !ok {
		return
	}
	report, err := readTestCoverage(r.Context(), h, scan.ID)
	if err != nil {
		response.WriteError(w, 500, "server_error", "cannot load measured coverage")
		return
	}
	type fileRow struct {
		Path           string                     `json:"path"`
		Metrics        map[string]coverage.Metric `json:"metrics"`
		UncoveredLines []int                      `json:"uncovered_lines"`
		UncoveredCount int                        `json:"uncovered_count"`
	}
	rows := []fileRow{}
	query, metric := strings.ToLower(r.URL.Query().Get("q")), defaultString(r.URL.Query().Get("metric"), "lines")
	if !slices.Contains([]string{"lines", "branches", "statements", "functions"}, metric) {
		response.WriteError(w, 400, "bad_request", "invalid coverage metric")
		return
	}
	if report != nil {
		files := report.Files
		if r.URL.Query().Get("scope") == "changed" {
			files = nil
			if report.Changed != nil {
				files = report.Changed.Files
			}
		}
		for _, f := range files {
			if !strings.Contains(strings.ToLower(f.Path), query) {
				continue
			}
			uncovered := []int{}
			for line, covered := range f.Lines {
				if !covered {
					uncovered = append(uncovered, line)
				}
			}
			sort.Ints(uncovered)
			count := len(uncovered)
			if count > 100 {
				uncovered = uncovered[:100]
			}
			rows = append(rows, fileRow{f.Path, f.Metrics, uncovered, count})
		}
	}
	sortField, desc := r.URL.Query().Get("sort"), r.URL.Query().Get("order") == "desc"
	sort.Slice(rows, func(i, j int) bool {
		cmp := strings.Compare(rows[i].Path, rows[j].Path)
		if sortField != "path" {
			a, aok := rows[i].Metrics[metric]
			b, bok := rows[j].Metrics[metric]
			av, bv := -1.0, -1.0
			if aok && a.Total > 0 {
				av = a.Percent()
			}
			if bok && b.Total > 0 {
				bv = b.Percent()
			}
			if av < bv {
				cmp = -1
			} else if av > bv {
				cmp = 1
			}
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
	page, perPage := parsePagination(r)
	start, end := paginateSlice(len(rows), page, perPage)
	response.WriteJSON(w, 200, response.ListResponse{Data: rows[start:end], Meta: response.ListMeta{Total: len(rows), Page: page, PerPage: perPage}})
}

// Keep errors returned by storage distinct from missing evidence.
func coverageForGate(ctx context.Context, h *Handler, scan *models.Scan, policy gates.Policy, eval *gates.Evaluation) error {
	for _, rule := range policy.Rules {
		if rule.TestCoverage != nil {
			report, err := readTestCoverage(ctx, h, scan.ID)
			if err != nil {
				return fmt.Errorf("load test coverage: %w", err)
			}
			commit := scan.CommitSHA
			if scan.DirtyState != "" && scan.DirtyState != "clean" {
				commit = ""
			}
			gates.ApplyCoverage(eval, policy, report, commit)
			break
		}
	}
	return nil
}
