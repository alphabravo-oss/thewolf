package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/scan/scorer"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
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
	if err != nil || repo == nil || repo.UserID != claims.UserID {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
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
	toolsRun := parsed.Tools
	toolsJSON, _ := json.Marshal(toolsRun)
	scan := &models.Scan{
		ID:              scanID,
		UserID:          claims.UserID,
		RepoID:          repo.ID,
		Branch:          branch,
		SourceType:      repo.SourceType,
		SourcePath:      repo.SourcePath,
		Status:          models.ScanStatusRunning,
		Phase:           "importing",
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

	complete := false
	defer func() {
		if complete {
			return
		}
		// Client cancellation must not leave a partially imported scan successful.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		scan.Status, scan.Phase = models.ScanStatusFailed, "failed"
		if scan.FailureCode == "" {
			scan.FailureCode, scan.FailureMessage = "import_failed", "SARIF import did not complete"
		}
		if _, err := h.Store.FinalizeScan(ctx, scan, ""); err != nil {
			wolflog.Error().Err(err).Str("scan_id", scan.ID).Msg("failed to record import failure")
		}
	}()

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
	if err := h.Store.CaptureScanIgnorePolicy(r.Context(), scanID, "", repo.SourcePath); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to capture scan ignore policy")
		return
	}
	clusters := runner.Deduplicate(findings)
	scorer.ScoreFindings(clusters)
	if err := h.Store.FinalizeFindingClusters(r.Context(), scanID, "", clusters); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to finalize imported findings")
		return
	}
	if err := persistBaselineState(r.Context(), h, scan); err != nil {
		scan.FailureCode, scan.FailureMessage = "baseline_persist_failed", truncateScanError(err.Error())
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to persist import baseline")
		return
	}
	if err := recordSARIFImportScannerRuns(r.Context(), h, scanID, toolsRun, findings, source, now); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to persist import tool evidence")
		return
	}
	if _, err := publishScanArtifact(r.Context(), h, scan.ID, models.ArtifactSARIF, "imported.sarif", bytes.NewReader(data), nil); err != nil {
		scan.FailureCode, scan.FailureMessage = "artifact_persist_failed", truncateScanError(err.Error())
		response.WriteError(w, http.StatusInternalServerError, "artifact_error", "failed to publish imported SARIF")
		return
	}
	manifest := report.Manifest{
		Source: scanSourceProvenance(scan), RepoName: repo.Name, RepoPath: repo.SourcePath,
		RepoCommit: scan.CommitSHA, Branch: scan.Branch, StartedAt: now, FinishedAt: now,
		WolfVersion: "sarif-import", Detection: report.DetectionSummary{Languages: []string{}}, ScannersRun: toolsRun,
	}
	config := report.ReportConfig{RepoName: repo.Name, Branch: scan.Branch, ToolsRun: toolsRun}
	scan.Status, scan.Phase = models.ScanStatusCompleted, "completed"
	if _, _, err := publishScanResults(r.Context(), h, scan, artifactDirForScan(r.Context(), h, scan), config, manifest, db.ScanCompletion{SARIFImport: imp}); err != nil {
		scan.FailureCode, scan.FailureMessage = "scan_completion_failed", truncateScanError(err.Error())
		if isDatabaseUniquenessError(err) {
			response.WriteError(w, http.StatusConflict, "conflict", "SARIF import already exists")
		} else if errors.Is(err, errScanArtifactPublication) {
			scan.FailureCode = "artifact_persist_failed"
			response.WriteError(w, http.StatusInternalServerError, "artifact_error", "failed to publish imported artifacts")
		} else {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to complete import")
		}
		return
	}
	complete = true
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: sarifImportResponse{
		Import:       *imp,
		Scan:         *scan,
		FindingCount: scan.FindingCount,
	}})
}

func recordSARIFImportScannerRuns(ctx context.Context, h *Handler, scanID string, toolsRun []string, findings []models.Finding, source string, ts time.Time) error {
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
		if err := h.Store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{
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
		}); err != nil {
			return err
		}
	}
	return nil
}
