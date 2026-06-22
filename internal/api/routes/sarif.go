package routes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

type sarifImportRequest struct {
	RepoID string `json:"repo_id"`
	Branch string `json:"branch,omitempty"`
	Source string `json:"source,omitempty"`
	SARIF  string `json:"sarif"`
}

type sarifImportResponse struct {
	Import       models.SARIFImport `json:"import"`
	Scan         models.Scan        `json:"scan"`
	FindingCount int                `json:"finding_count"`
}

const maxSARIFImportRequestBytes = sarifio.MaxImportBytes + (1 << 20)

func ImportSARIF(w http.ResponseWriter, r *http.Request) {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxSARIFImportRequestBytes)
	var req sarifImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.RepoID == "" || req.SARIF == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "repo_id and sarif are required")
		return
	}
	repo, err := h.Store.GetRepoByID(r.Context(), req.RepoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if repo.UserID != claims.UserID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "repo does not belong to current user")
		return
	}

	data := []byte(req.SARIF)
	parsed, err := sarifio.Import(data)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_sarif", err.Error())
		return
	}
	now := time.Now().UTC()
	branch := req.Branch
	if branch == "" {
		branch = repo.DefaultBranch
	}
	if branch == "" {
		branch = "main"
	}
	source := req.Source
	if source == "" {
		source = "sarif-import"
	}
	scanID := uuid.New().String()
	toolsRun := toolsFromFindings(parsed.Findings)
	toolsJSON, _ := json.Marshal(toolsRun)
	scan := &models.Scan{
		ID:              scanID,
		UserID:          claims.UserID,
		RepoID:          repo.ID,
		Branch:          branch,
		SourceType:      repo.SourceType,
		SourcePath:      repo.SourcePath,
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   string(toolsJSON),
		ToolsCompleted:  string(toolsJSON),
		ToolsFailed:     "[]",
		ToolsErrors:     "{}",
		FindingCount:    len(parsed.Findings),
		CoverageSummary: "{}",
		StartedAt:       &now,
		CompletedAt:     &now,
	}
	if err := h.Store.CreateScan(r.Context(), scan); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create import scan")
		return
	}

	findings := parsed.Findings
	for i := range findings {
		findings[i].ID = uuid.New().String()
		findings[i].ScanID = scanID
		findings[i].RepoID = repo.ID
		if findings[i].SourceKind == "" {
			findings[i].SourceKind = "sarif_import"
		}
		if findings[i].SourceRef == "" {
			findings[i].SourceRef = source
		}
	}
	if len(findings) > 0 {
		if err := h.Store.CreateFindings(r.Context(), findings); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create imported findings")
			return
		}
	}

	checksum := sha256.Sum256(data)
	checksumHex := hex.EncodeToString(checksum[:])
	imp := &models.SARIFImport{
		ID:             uuid.New().String(),
		RepoID:         repo.ID,
		ScanID:         scanID,
		Source:         source,
		ChecksumSHA256: checksumHex,
		ResultCount:    parsed.ResultCount,
		ImportedCount:  len(findings),
		CreatedBy:      claims.UserID,
	}
	if err := h.Store.CreateSARIFImport(r.Context(), imp); err != nil {
		response.WriteError(w, http.StatusConflict, "conflict", "SARIF import already exists or could not be recorded")
		return
	}

	recordSARIFImportScannerRuns(r.Context(), h, scanID, toolsRun, findings, source, now)
	writeSARIFImportArtifacts(r.Context(), h, scan, repo, source, data, findings, toolsRun, now)
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: sarifImportResponse{
		Import:       *imp,
		Scan:         *scan,
		FindingCount: len(findings),
	}})
}

func recordSARIFImportScannerRuns(ctx context.Context, h *Handler, scanID string, toolsRun []string, findings []models.Finding, source string, ts time.Time) {
	counts := make(map[string]int, len(toolsRun))
	categories := make(map[string]string, len(toolsRun))
	for _, finding := range findings {
		counts[finding.ToolName]++
		if categories[finding.ToolName] == "" {
			categories[finding.ToolName] = string(finding.Category)
		}
	}
	command, _ := json.Marshal(map[string]string{
		"source": source,
		"type":   "sarif_import",
	})
	for _, tool := range toolsRun {
		upsertScannerRunRecord(ctx, h, &models.ScannerRunRecord{
			ID:            uuid.New().String(),
			ScanID:        scanID,
			ToolName:      tool,
			Status:        "imported",
			Category:      categories[tool],
			CommandJSON:   string(command),
			FindingCount:  counts[tool],
			ParserStatus:  "parsed",
			ParserMessage: "imported from SARIF",
			StartedAt:     &ts,
			FinishedAt:    &ts,
		})
	}
}

func writeSARIFImportArtifacts(ctx context.Context, h *Handler, scan *models.Scan, repo *models.Repo, source string, data []byte, findings []models.Finding, toolsRun []string, ts time.Time) {
	dir := artifactDirForScan(ctx, h, scan)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	rawPath := filepath.Join(dir, "imported.sarif")
	if err := os.WriteFile(rawPath, data, 0o600); err == nil {
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactSARIF, rawPath)
	}
	mfst := report.Manifest{
		ScanID:      scan.ID,
		Source:      scanSourceProvenance(scan),
		RepoName:    repo.Name,
		RepoPath:    repo.SourcePath,
		RepoCommit:  scan.CommitSHA,
		Branch:      scan.Branch,
		StartedAt:   ts,
		FinishedAt:  ts,
		WolfVersion: "sarif-import",
		Detection: report.DetectionSummary{
			Languages: []string{},
		},
		ScannersRun: toolsRun,
		Counts:      report.CountFindings(0, findings),
	}
	if w, err := report.WriteAll(dir, report.ReportConfig{
		ScanID:   scan.ID,
		RepoName: repo.Name,
		Branch:   scan.Branch,
		Findings: findings,
		ToolsRun: toolsRun,
	}, mfst); err == nil {
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactJSON, w.FindingsJSON)
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactMarkdown, w.RawMarkdown)
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactSARIF, w.CombinedSARIF)
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactManifest, w.Manifest)
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactMarkdown, w.FixHigh)
		recordScanArtifact(ctx, h, scan.ID, models.ArtifactMarkdown, w.FixAll)
	}
	_ = source
}

func toolsFromFindings(findings []models.Finding) []string {
	seen := map[string]bool{}
	for _, finding := range findings {
		if finding.ToolName != "" {
			seen[finding.ToolName] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tool := range seen {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}
