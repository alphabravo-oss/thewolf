package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// testEnv bundles the router, store, and token for scan/finding/fix tests.
type testEnv struct {
	Router *chi.Mux
	Store  db.Store
	Token  string
	UserID string
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}

	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	routes.SetHandler(store, nil)

	r := chi.NewRouter()
	r.Post("/api/auth/register", routes.Register)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		// Repos
		r.Post("/api/repos", routes.CreateRepo)
		r.Delete("/api/repos/{id}", routes.DeleteRepo)
		r.Post("/api/collections", routes.CreateCollection)
		r.Delete("/api/collections/{id}", routes.DeleteCollection)
		r.Get("/api/repos/{id}/baselines", routes.ListRepoBaselines)
		r.Post("/api/repos/{id}/baselines", routes.CreateRepoBaseline)
		// Scans
		r.Post("/api/scans", routes.CreateScan)
		r.Post("/api/scans/preflight", routes.ScanPreflight)
		r.Get("/api/scans", routes.ListScans)
		r.Get("/api/scans/orphans", routes.ListOrphanScans)
		r.Delete("/api/scans/orphans", routes.PurgeOrphanScans)
		r.Get("/api/scans/{id}", routes.GetScan)
		r.Get("/api/scans/{id}/findings", routes.GetScanFindings)
		r.Get("/api/scans/{id}/manifest", routes.GetScanManifest)
		r.Get("/api/scans/{id}/gate", routes.GetScanGate)
		r.Get("/api/scans/{id}/diff", routes.GetScanDiff)
		r.Post("/api/scans/{id}/compare", routes.CompareScanToBaseline)
		r.Get("/api/scans/{id}/compare/{compareId}", routes.CompareScan)
		r.Get("/api/scans/{id}/artifacts/{artifactId}/download", routes.DownloadArtifact)
		r.Get("/api/scans/{id}/tools", routes.GetScanTools)
		r.Get("/api/scans/{id}/scanner-runs", routes.GetScannerRunRecords)
		r.Get("/api/scans/{id}/stream", routes.StreamScan)
		r.Delete("/api/scans/{id}", routes.CancelScan)
		// Source credentials
		r.Get("/api/credentials", routes.ListCredentials)
		r.Post("/api/credentials", routes.CreateCredential)
		r.Get("/api/credentials/{id}", routes.GetCredential)
		r.Delete("/api/credentials/{id}", routes.DeleteCredential)
		// Generic secrets
		r.Get("/api/config/secrets", routes.ListSecrets)
		r.Post("/api/config/secrets", routes.CreateSecret)
		// Findings
		r.Get("/api/findings", routes.ListFindings)
		r.Get("/api/findings/{id}", routes.GetFinding)
		r.Put("/api/findings/{id}/status", routes.UpdateFindingStatus)
		r.Get("/api/findings/trends", routes.FindingTrends)
		// SARIF
		r.Post("/api/sarif/import", routes.ImportSARIF)
		// Suppressions
		r.Get("/api/suppressions", routes.ListSuppressions)
		r.Post("/api/suppressions", routes.CreateSuppression)
		r.Post("/api/suppressions/preview", routes.PreviewSuppression)
		r.Delete("/api/suppressions/{id}", routes.RevokeSuppression)
		// Policies
		r.Get("/api/policies", routes.ListPolicies)
		r.Post("/api/policies", routes.CreatePolicy)
		r.Put("/api/policies/{id}", routes.UpdatePolicy)
		// Fixes
		r.Post("/api/fixes", routes.CreateFix)
		r.Get("/api/fixes", routes.ListFixes)
		r.Get("/api/fixes/engines", routes.ListFixEngines)
		r.Post("/api/fixes/consoles", routes.CreateFixerConsole)
		r.Get("/api/fixes/consoles/{id}", routes.GetFixerConsole)
		r.Get("/api/fixes/consoles/{id}/stream", routes.StreamFixerConsole)
		r.Post("/api/fixes/consoles/{id}/input", routes.InputFixerConsole)
		r.Delete("/api/fixes/consoles/{id}", routes.CancelFixerConsole)
		r.Get("/api/scans/{id}/lineage", routes.GetScanLineage)
		r.Post("/api/remediations/{id}/accept", routes.AcceptRemediation)
		r.Get("/api/fixes/{id}", routes.GetFix)
		r.Get("/api/fixes/{id}/diff", routes.GetFixDiff)
		r.Post("/api/fixes/{id}/resume", routes.ResumeFix)
		r.Delete("/api/fixes/{id}", routes.CancelFix)
		// Agents

		// Fleet aggregates
		r.Get("/api/fleet/posture", routes.FleetPosture)
		r.Get("/api/fleet/inventory", routes.FleetInventory)
		r.Get("/api/fleet/needs-attention", routes.FleetNeedsAttention)
		r.Get("/api/findings/aggregate", routes.FindingsAggregate)
		r.Get("/api/findings/by-repo", routes.FindingsByRepo)
	})

	// Register user
	body, _ := json.Marshal(map[string]string{
		"email":    "testuser@example.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			AccessToken string `json:"access_token"`
			User        struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	return &testEnv{
		Router: r,
		Store:  store,
		Token:  resp.Data.AccessToken,
		UserID: resp.Data.User.ID,
	}
}

func (e *testEnv) doRequest(method, path string, body interface{}) *httptest.ResponseRecorder {
	var reqBody *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	w := httptest.NewRecorder()
	e.Router.ServeHTTP(w, req)
	return w
}

func (e *testEnv) createRepo(t *testing.T) string {
	t.Helper()
	w := e.doRequest(http.MethodPost, "/api/repos", map[string]string{
		"name":        "test-repo",
		"source_type": "local",
		"source_path": "/tmp/test-repo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.Data.ID
}

func TestRepoBaselinesAPI(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}

	w := env.doRequest(http.MethodPost, "/api/repos/"+repoID+"/baselines", map[string]string{
		"name":    "last-good",
		"scan_id": scanID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create baseline: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var createResp struct {
		Data models.ScanBaseline `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Data.Branch != "main" || createResp.Data.Strategy != "named" {
		t.Fatalf("unexpected baseline response: %+v", createResp.Data)
	}

	w = env.doRequest(http.MethodGet, "/api/repos/"+repoID+"/baselines?branch=main", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list baselines: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Data []models.ScanBaseline `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Name != "last-good" {
		t.Fatalf("unexpected baselines: %+v", listResp.Data)
	}
}

func TestGetScanManifestFromArtifact(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"scan_id":"`+scanID+`","scanner_plan":{"summary":{"run_count":1}}}`), 0o600); err != nil {
		t.Fatalf("write manifest fixture: %v", err)
	}
	now := time.Now()
	if err := env.Store.CreateScanArtifact(context.Background(), &models.ScanArtifact{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ArtifactType: models.ArtifactManifest,
		FilePath:     manifestPath,
		FileSize:     64,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("CreateScanArtifact failed: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/manifest", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("manifest: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("manifest response is not JSON: %v", err)
	}
	if got["scan_id"] != scanID {
		t.Fatalf("scan_id = %v, want %s", got["scan_id"], scanID)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", w.Header().Get("Content-Type"))
	}
}

func TestGetScanManifestFallback(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		SourceType:      models.SourceTypeLocal,
		SourcePath:      "/tmp/test-repo",
		CommitSHA:       "abc123",
		DirtyState:      "clean",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   `["gosec"]`,
		ToolsCompleted:  `["gosec"]`,
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}
	if err := env.Store.CreateFinding(context.Background(), &models.Finding{
		ID:                uuid.New().String(),
		ScanID:            scanID,
		RepoID:            repoID,
		Fingerprint:       "legacy-fp",
		StableFingerprint: "stable-fp",
		ToolName:          "gosec",
		Category:          models.CategorySAST,
		Severity:          models.SeverityHigh,
		Title:             "SQL injection",
		FilePath:          "app/db.go",
		LineStart:         10,
		RuleID:            "G201",
		Status:            models.StatusOpen,
	}); err != nil {
		t.Fatalf("CreateFinding failed: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/manifest", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("manifest fallback: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		ScanID      string   `json:"scan_id"`
		ScannersRun []string `json:"scanners_run"`
		Source      struct {
			Kind             string `json:"kind"`
			RepoID           string `json:"repo_id"`
			RepoPath         string `json:"repo_path"`
			Branch           string `json:"branch"`
			CommitSHA        string `json:"commit_sha"`
			DirtyState       string `json:"dirty_state"`
			SnapshotStrategy string `json:"snapshot_strategy"`
		} `json:"source"`
		Counts struct {
			AfterDedupe  int `json:"after_dedupe"`
			HighSeverity int `json:"high_severity"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("manifest response is not JSON: %v", err)
	}
	if got.ScanID != scanID || len(got.ScannersRun) != 1 || got.ScannersRun[0] != "gosec" {
		t.Fatalf("unexpected manifest fallback: %+v", got)
	}
	if got.Counts.AfterDedupe != 1 || got.Counts.HighSeverity != 1 {
		t.Fatalf("unexpected counts: %+v", got.Counts)
	}
	if got.Source.Kind != "local_path" || got.Source.RepoID != repoID || got.Source.CommitSHA != "abc123" || got.Source.SnapshotStrategy != "working_tree" {
		t.Fatalf("unexpected source provenance: %+v", got.Source)
	}
}

func TestImportSARIFAPI(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	sarif := `{
	  "version": "2.1.0",
	  "runs": [{
	    "tool": {"driver": {"name": "semgrep", "rules": [{
	      "id": "go.sql",
	      "shortDescription": {"text": "SQL Injection"},
	      "properties": {"cweId": "CWE-89", "category": "sast", "fineCategory": "sql-injection"}
	    }]}},
	    "results": [{
	      "ruleId": "go.sql",
	      "level": "error",
	      "message": {"text": "unsafe query"},
	      "locations": [{"physicalLocation": {"artifactLocation": {"uri": "app/db.go"}, "region": {"startLine": 42}}}],
	      "partialFingerprints": {"wolfStableFingerprint": "stable-import"}
	    }]
	  }]
	}`

	w := env.doRequest(http.MethodPost, "/api/sarif/import", map[string]string{
		"repo_id": repoID,
		"branch":  "feature/sarif",
		"source":  "fixture",
		"sarif":   sarif,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("sarif import: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Import models.SARIFImport `json:"import"`
			Scan   models.Scan        `json:"scan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if resp.Data.Scan.Status != models.ScanStatusCompleted || resp.Data.Import.ImportedCount != 1 {
		t.Fatalf("unexpected import response: %+v", resp.Data)
	}
	findings, err := env.Store.ListFindingsByScan(context.Background(), resp.Data.Scan.ID)
	if err != nil {
		t.Fatalf("ListFindingsByScan failed: %v", err)
	}
	if len(findings) != 1 || findings[0].StableFingerprint != "stable-import" || findings[0].SourceKind != "sarif_import" {
		t.Fatalf("unexpected imported findings: %+v", findings)
	}
	imports, err := env.Store.ListSARIFImportsByRepo(context.Background(), repoID)
	if err != nil {
		t.Fatalf("ListSARIFImportsByRepo failed: %v", err)
	}
	if len(imports) != 1 || imports[0].ScanID != resp.Data.Scan.ID || imports[0].ChecksumSHA256 == "" {
		t.Fatalf("unexpected import metadata: %+v", imports)
	}
	runs, err := env.Store.ListScannerRunRecords(context.Background(), resp.Data.Scan.ID)
	if err != nil {
		t.Fatalf("ListScannerRunRecords failed: %v", err)
	}
	if len(runs) != 1 || runs[0].ToolName != "semgrep" || runs[0].Status != "imported" || runs[0].FindingCount != 1 || runs[0].ParserStatus != "parsed" {
		t.Fatalf("unexpected SARIF scanner run records: %+v", runs)
	}
	artifacts, err := env.Store.ListScanArtifacts(context.Background(), resp.Data.Scan.ID)
	if err != nil {
		t.Fatalf("ListScanArtifacts failed: %v", err)
	}
	hasSARIF := false
	hasManifest := false
	for _, artifact := range artifacts {
		if artifact.ArtifactType == models.ArtifactSARIF && filepath.Base(artifact.FilePath) == "imported.sarif" {
			hasSARIF = artifact.ChecksumSHA256 != ""
		}
		if artifact.ArtifactType == models.ArtifactManifest {
			hasManifest = true
		}
	}
	if !hasSARIF || !hasManifest {
		t.Fatalf("expected SARIF and manifest artifacts, got %+v", artifacts)
	}
}

func TestScannerRunRecordsAPI(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	started := time.Now().UTC()
	finished := started.Add(1500 * time.Millisecond)
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   `["gosec"]`,
		ToolsCompleted:  `["gosec"]`,
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}
	if err := env.Store.UpsertScannerRunRecord(context.Background(), &models.ScannerRunRecord{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ToolName:     "gosec",
		Status:       "completed",
		Category:     "sast",
		Image:        "ghcr.io/alphabravo/scanner-gosec@sha256:abc",
		ImageDigest:  "sha256:abc",
		CommandJSON:  `{"argv":["gosec","./..."]}`,
		DurationMS:   1500,
		FindingCount: 2,
		ParserStatus: "parsed",
		StartedAt:    &started,
		FinishedAt:   &finished,
	}); err != nil {
		t.Fatalf("UpsertScannerRunRecord failed: %v", err)
	}
	if err := env.Store.UpsertScannerRunRecord(context.Background(), &models.ScannerRunRecord{
		ID:            uuid.New().String(),
		ScanID:        scanID,
		ToolName:      "trivy",
		Status:        "skipped",
		Category:      "sca",
		CommandJSON:   "{}",
		ErrorMessage:  "tool unavailable",
		ParserStatus:  "not_run",
		ParserMessage: "tool unavailable",
	}); err != nil {
		t.Fatalf("UpsertScannerRunRecord skipped failed: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/scanner-runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("scanner-runs: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var runsResp struct {
		Data []models.ScannerRunRecord `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode scanner-runs response: %v", err)
	}
	if runsResp.Meta.Total != 2 || len(runsResp.Data) != 2 {
		t.Fatalf("unexpected scanner-runs response: %+v", runsResp)
	}

	w = env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/tools", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("scan tools: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var toolsResp struct {
		Data []struct {
			Name   string                   `json:"name"`
			Status string                   `json:"status"`
			Run    *models.ScannerRunRecord `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &toolsResp); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	seen := map[string]string{}
	for _, tool := range toolsResp.Data {
		if tool.Run == nil {
			t.Fatalf("expected run record for tool %+v", tool)
		}
		seen[tool.Name] = tool.Status
	}
	if seen["gosec"] != "completed" || seen["trivy"] != "skipped" {
		t.Fatalf("unexpected tool statuses: %+v", seen)
	}
}

func TestDownloadArtifactRejectsCrossUserScan(t *testing.T) {
	env := setupTestEnv(t)
	otherUserID := uuid.New().String()
	if err := env.Store.CreateUser(context.Background(), &models.User{
		ID:           otherUserID,
		Email:        "other@example.com",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser other failed: %v", err)
	}
	repoID := uuid.New().String()
	if err := env.Store.CreateRepo(context.Background(), &models.Repo{
		ID:            repoID,
		UserID:        otherUserID,
		Name:          "other-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/other-repo",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo other failed: %v", err)
	}
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          otherUserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan other failed: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "raw.log")
	if err := os.WriteFile(artifactPath, []byte("secret raw output"), 0o600); err != nil {
		t.Fatalf("write artifact fixture: %v", err)
	}
	artifactID := uuid.New().String()
	if err := env.Store.CreateScanArtifact(context.Background(), &models.ScanArtifact{
		ID:           artifactID,
		ScanID:       scanID,
		ArtifactType: models.ArtifactLog,
		FilePath:     artifactPath,
		FileSize:     17,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateScanArtifact failed: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/artifacts/"+artifactID+"/download", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("download cross-user artifact: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSuppressionsAPI(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	findingID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}
	if err := env.Store.CreateFinding(context.Background(), &models.Finding{
		ID:                findingID,
		ScanID:            scanID,
		RepoID:            repoID,
		Fingerprint:       "legacy-fp",
		StableFingerprint: "stable-fp",
		ToolName:          "gosec",
		Category:          models.CategorySAST,
		Severity:          models.SeverityHigh,
		Title:             "SQL injection",
		FilePath:          "app/db.go",
		LineStart:         10,
		RuleID:            "G201",
		FineCategory:      "sql-injection",
		Status:            models.StatusOpen,
		SARIFData:         "{}",
	}); err != nil {
		t.Fatalf("CreateFinding failed: %v", err)
	}

	w := env.doRequest(http.MethodPost, "/api/suppressions/preview", map[string]string{
		"repo_id":     repoID,
		"scope_type":  "fine_category",
		"scope_value": "sql-injection",
		"reason":      "legacy accepted risk",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("preview suppression: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var previewResp struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &previewResp)
	if previewResp.Data.Count != 1 {
		t.Fatalf("preview count = %d, want 1", previewResp.Data.Count)
	}

	w = env.doRequest(http.MethodPost, "/api/suppressions", map[string]string{
		"finding_id": findingID,
		"reason":     "accepted during migration",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create suppression: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var createResp struct {
		Data models.FindingSuppression `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)
	if createResp.Data.ScopeType != models.SuppressionScopeStableFingerprint || createResp.Data.ScopeValue != "stable-fp" {
		t.Fatalf("unexpected suppression: %+v", createResp.Data)
	}

	finding, err := env.Store.GetFindingByID(context.Background(), findingID)
	if err != nil {
		t.Fatalf("GetFindingByID failed: %v", err)
	}
	if !finding.Suppressed || finding.SuppressionID != createResp.Data.ID {
		t.Fatalf("finding was not marked suppressed: %+v", finding)
	}

	w = env.doRequest(http.MethodGet, "/api/suppressions?repo_id="+repoID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list suppressions: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = env.doRequest(http.MethodDelete, "/api/suppressions/"+createResp.Data.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke suppression: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	finding, err = env.Store.GetFindingByID(context.Background(), findingID)
	if err != nil {
		t.Fatalf("GetFindingByID after revoke failed: %v", err)
	}
	if finding.Suppressed || finding.SuppressionID != "" {
		t.Fatalf("finding suppression was not cleared: %+v", finding)
	}
}

func TestScanGateAPI(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}
	if err := env.Store.CreateFinding(context.Background(), &models.Finding{
		ID:          uuid.New().String(),
		ScanID:      scanID,
		RepoID:      repoID,
		Fingerprint: "gate-fp",
		ToolName:    "trivy",
		Category:    models.CategorySCA,
		Severity:    models.SeverityHigh,
		Title:       "Critical package vulnerability",
		FilePath:    "package-lock.json",
		RuleID:      "GHSA-test",
		Status:      models.StatusOpen,
		SARIFData:   "{}",
	}); err != nil {
		t.Fatalf("CreateFinding failed: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/scans/"+scanID+"/gate", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("gate eval: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var gateResp struct {
		Data struct {
			Evaluation struct {
				Status string `json:"status"`
			} `json:"evaluation"`
			Result models.QualityGateResult `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &gateResp); err != nil {
		t.Fatalf("decode gate response: %v", err)
	}
	if gateResp.Data.Evaluation.Status != "fail" || gateResp.Data.Result.Status != "fail" {
		t.Fatalf("expected fail gate, got %+v", gateResp.Data)
	}

	results, err := env.Store.ListQualityGateResults(context.Background(), scanID)
	if err != nil {
		t.Fatalf("ListQualityGateResults failed: %v", err)
	}
	if len(results) != 1 || results[0].Status != "fail" {
		t.Fatalf("gate result was not persisted: %+v", results)
	}
	artifacts, err := env.Store.ListScanArtifacts(context.Background(), scanID)
	if err != nil {
		t.Fatalf("ListScanArtifacts failed: %v", err)
	}
	var gateArtifact *models.ScanArtifact
	for i := range artifacts {
		if filepath.Base(artifacts[i].FilePath) == "gate-result.json" {
			gateArtifact = &artifacts[i]
			break
		}
	}
	if gateArtifact == nil {
		t.Fatalf("gate-result.json artifact was not recorded: %+v", artifacts)
	}
	if gateArtifact.ChecksumSHA256 == "" || gateArtifact.RedactionLevel != "internal_report" {
		t.Fatalf("gate artifact metadata missing: %+v", gateArtifact)
	}
	if _, err := os.Stat(gateArtifact.FilePath); err != nil {
		t.Fatalf("gate artifact missing on disk: %v", err)
	}
}

func TestCompareScanWritesDiffArtifact(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	baselineID := uuid.New().String()
	currentID := uuid.New().String()
	for _, scanID := range []string{baselineID, currentID} {
		if err := env.Store.CreateScan(context.Background(), &models.Scan{
			ID:              scanID,
			UserID:          env.UserID,
			RepoID:          repoID,
			Branch:          "main",
			Status:          models.ScanStatusCompleted,
			ToolsSelected:   `["gosec"]`,
			ToolsCompleted:  `["gosec"]`,
			ToolsFailed:     "[]",
			CoverageSummary: "{}",
		}); err != nil {
			t.Fatalf("CreateScan failed: %v", err)
		}
	}
	if err := env.Store.CreateFinding(context.Background(), &models.Finding{
		ID:                uuid.New().String(),
		ScanID:            baselineID,
		RepoID:            repoID,
		Fingerprint:       "fixed-fp",
		StableFingerprint: "fixed-fp",
		ToolName:          "gosec",
		Category:          models.CategorySAST,
		Severity:          models.SeverityHigh,
		Title:             "Fixed issue",
		FilePath:          "app/old.go",
		LineStart:         10,
		RuleID:            "G201",
		Status:            models.StatusOpen,
	}); err != nil {
		t.Fatalf("CreateFinding baseline failed: %v", err)
	}
	if err := env.Store.CreateFinding(context.Background(), &models.Finding{
		ID:                uuid.New().String(),
		ScanID:            currentID,
		RepoID:            repoID,
		Fingerprint:       "new-fp",
		StableFingerprint: "new-fp",
		ToolName:          "gosec",
		Category:          models.CategorySAST,
		Severity:          models.SeverityHigh,
		Title:             "New issue",
		FilePath:          "app/new.go",
		LineStart:         20,
		RuleID:            "G202",
		Status:            models.StatusOpen,
	}); err != nil {
		t.Fatalf("CreateFinding current failed: %v", err)
	}

	w := env.doRequest(http.MethodPost, "/api/scans/"+currentID+"/compare", map[string]string{
		"baseline_scan_id": baselineID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("compare: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	artifacts, err := env.Store.ListScanArtifacts(context.Background(), currentID)
	if err != nil {
		t.Fatalf("ListScanArtifacts failed: %v", err)
	}
	var diffArtifact *models.ScanArtifact
	for i := range artifacts {
		if filepath.Base(artifacts[i].FilePath) == "diff.json" {
			diffArtifact = &artifacts[i]
			break
		}
	}
	if diffArtifact == nil {
		t.Fatalf("diff.json artifact was not recorded: %+v", artifacts)
	}
	if diffArtifact.ChecksumSHA256 == "" || diffArtifact.RedactionLevel != "internal_report" {
		t.Fatalf("diff artifact metadata missing: %+v", diffArtifact)
	}
	data, err := os.ReadFile(diffArtifact.FilePath)
	if err != nil {
		t.Fatalf("read diff artifact: %v", err)
	}
	var diffBody struct {
		Summary struct {
			NewCount   int `json:"new_count"`
			FixedCount int `json:"fixed_count"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &diffBody); err != nil {
		t.Fatalf("diff artifact is not JSON: %v", err)
	}
	if diffBody.Summary.NewCount != 1 || diffBody.Summary.FixedCount != 1 {
		t.Fatalf("unexpected diff summary: %+v", diffBody.Summary)
	}

	w = env.doRequest(http.MethodGet, "/api/scans/"+currentID+"/diff?baseline_scan_id="+baselineID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("diff: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var diffResp struct {
		Data struct {
			Summary struct {
				NewCount   int `json:"new_count"`
				FixedCount int `json:"fixed_count"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &diffResp); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	if diffResp.Data.Summary.NewCount != 1 || diffResp.Data.Summary.FixedCount != 1 {
		t.Fatalf("unexpected diff response: %+v", diffResp.Data.Summary)
	}
}

func TestCompareScanRejectsIncompatibleSources(t *testing.T) {
	env := setupTestEnv(t)
	repo1 := env.createRepo(t)
	w := env.doRequest(http.MethodPost, "/api/repos", map[string]string{
		"name":        "other-repo",
		"source_type": "local",
		"source_path": "/tmp/other-repo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create second repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repoResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &repoResp); err != nil {
		t.Fatalf("decode repo response: %v", err)
	}
	repo2 := repoResp.Data.ID
	baselineID := uuid.New().String()
	currentID := uuid.New().String()
	for _, scan := range []struct {
		id     string
		repoID string
		path   string
	}{
		{id: baselineID, repoID: repo1, path: "/tmp/test-repo"},
		{id: currentID, repoID: repo2, path: "/tmp/other-repo"},
	} {
		if err := env.Store.CreateScan(context.Background(), &models.Scan{
			ID:              scan.id,
			UserID:          env.UserID,
			RepoID:          scan.repoID,
			Branch:          "main",
			SourceType:      models.SourceTypeLocal,
			SourcePath:      scan.path,
			Status:          models.ScanStatusCompleted,
			ToolsSelected:   "[]",
			ToolsCompleted:  "[]",
			ToolsFailed:     "[]",
			CoverageSummary: "{}",
		}); err != nil {
			t.Fatalf("CreateScan failed: %v", err)
		}
	}

	w = env.doRequest(http.MethodPost, "/api/scans/"+currentID+"/compare", map[string]string{
		"baseline_scan_id": baselineID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("compare incompatible sources: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "different repositories") {
		t.Fatalf("unexpected incompatible-source response: %s", w.Body.String())
	}
}

func TestPoliciesAPI(t *testing.T) {
	env := setupTestEnv(t)
	auth.RoleResolver = func(ctx context.Context, userID string) string {
		user, err := env.Store.GetUserByID(ctx, userID)
		if err != nil {
			return ""
		}
		return user.Role
	}
	t.Cleanup(func() { auth.RoleResolver = nil })

	w := env.doRequest(http.MethodPost, "/api/policies", map[string]any{
		"name":  "warn-medium",
		"scope": "global",
		"mode":  "warn",
		"rules": []map[string]any{
			{
				"id":       "warn-medium",
				"severity": []string{"medium"},
				"action":   "warn",
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create policy: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = env.doRequest(http.MethodGet, "/api/policies?scope=global", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list policies: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Data []models.QualityPolicy `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Name != "warn-medium" {
		t.Fatalf("unexpected policies: %+v", listResp.Data)
	}
}

// --- Scan tests ---

func TestCreateScan(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	w := env.doRequest(http.MethodPost, "/api/scans", map[string]string{
		"repo_id": repoID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			RepoID string `json:"repo_id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Data.ID == "" {
		t.Error("expected scan ID")
	}
	if resp.Data.Status != "pending" {
		t.Errorf("expected status pending, got %s", resp.Data.Status)
	}
	if resp.Data.RepoID != repoID {
		t.Errorf("expected repo_id %s, got %s", repoID, resp.Data.RepoID)
	}
}

func TestCreateScanMissingRepoID(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodPost, "/api/scans", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateScanIdempotencyReturnsOriginalAndRejectsConflict(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)

	request := func(branch string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"repo_id": repoID, "branch": branch})
		req := httptest.NewRequest(http.MethodPost, "/api/scans", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+env.Token)
		req.Header.Set("Idempotency-Key", "build-42")
		w := httptest.NewRecorder()
		env.Router.ServeHTTP(w, req)
		return w
	}
	first := request("main")
	second := request("main")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("idempotent create status: first=%d second=%d", first.Code, second.Code)
	}
	var firstBody, secondBody struct {
		Data models.Scan `json:"data"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)
	_ = json.Unmarshal(second.Body.Bytes(), &secondBody)
	if firstBody.Data.ID == "" || firstBody.Data.ID != secondBody.Data.ID {
		t.Fatalf("expected original scan, first=%q second=%q", firstBody.Data.ID, secondBody.Data.ID)
	}
	conflict := request("develop")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("expected idempotency conflict, got %d: %s", conflict.Code, conflict.Body.String())
	}
}

func TestQueuedScanStreamReplaysAfterLastEventIDAndClosesAtTerminal(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	scan := &models.Scan{
		ID: uuid.NewString(), UserID: env.UserID, RepoID: repoID, Branch: "main",
		Status: models.ScanStatusCompleted, Phase: "completed",
	}
	if err := env.Store.CreateScan(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"type":"scan_status","status":"pending"}`,
		`{"type":"scan_progress","tool_name":"semgrep","status":"running"}`,
		`{"type":"scan_complete","status":"completed"}`,
	} {
		if err := env.Store.AppendScanEvent(context.Background(), &models.ScanEvent{
			ScanID: scan.ID, EventType: "scan_event", DataJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/scans/"+scan.ID+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+env.Token)
	req.Header.Set("Last-Event-ID", "1")
	w := httptest.NewRecorder()
	env.Router.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "id: 1\n") {
		t.Fatalf("stream replayed acknowledged event: %s", body)
	}
	for _, expected := range []string{
		"id: 2\n",
		`"type":"scan_progress"`,
		"id: 3\n",
		`"type":"scan_complete"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream omitted %q: %s", expected, body)
		}
	}
}

func TestCreateScanWithGitSourceUpsertsVisibleRepo(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	env := setupTestEnv(t)
	defer env.Store.Close()
	body := map[string]interface{}{
		"source": map[string]interface{}{
			"kind": "git", "name": "wolf", "url": "https://github.com/alphabravo-oss/thewolf.git",
			"ref": "refs/heads/main",
		},
		"profile":       "targeted",
		"include_paths": []string{"internal/**"},
	}
	first := env.doRequest(http.MethodPost, "/api/scans", body)
	second := env.doRequest(http.MethodPost, "/api/scans", body)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("source create: first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	repos, err := env.Store.ListReposByUser(context.Background(), env.UserID)
	if err != nil || len(repos) != 1 {
		t.Fatalf("expected one upserted repo, repos=%v err=%v", repos, err)
	}
	if repos[0].SourceType != models.SourceTypeGit || repos[0].SourceFingerprint == "" {
		t.Fatalf("unexpected source repo: %#v", repos[0])
	}
	var created struct {
		Data models.Scan `json:"data"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &created)
	if created.Data.RepoID != repos[0].ID || created.Data.Status != models.ScanStatusPending {
		t.Fatalf("scan did not reference visible source repo: %#v", created.Data)
	}
}

func TestCreateScanWithOneShotSSHSourceUpsertsRepoAndRotatesCredential(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	defer env.Store.Close()

	createCredential := func(name, secret string) string {
		t.Helper()
		response := env.doRequest(http.MethodPost, "/api/credentials", map[string]interface{}{
			"type": "ssh_private_key", "name": name, "secret": secret,
			"known_hosts":   "192.0.2.10 ssh-ed25519 AAAATest",
			"allowed_hosts": []string{"192.0.2.10"},
		})
		if response.Code != http.StatusCreated {
			t.Fatalf("create credential: %d %s", response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data.ID
	}
	firstCredentialID := createCredential("first", "PRIVATE-KEY-ONE")
	sourceBody := func(credentialID string) map[string]interface{} {
		return map[string]interface{}{
			"source": map[string]interface{}{
				"kind": "ssh", "name": "remote-repo", "host": "192.0.2.10",
				"port": 22, "username": "scanner", "path": "/srv/repos/app",
				"base_path": "/srv/repos", "credential_id": credentialID,
				"known_hosts": "192.0.2.10 ssh-ed25519 AAAATest",
			},
		}
	}
	first := env.doRequest(http.MethodPost, "/api/scans", sourceBody(firstCredentialID))
	second := env.doRequest(http.MethodPost, "/api/scans", sourceBody(firstCredentialID))
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("SSH source create: first=%d %s second=%d %s",
			first.Code, first.Body.String(), second.Code, second.Body.String())
	}

	repos, err := env.Store.ListReposByUser(context.Background(), env.UserID)
	if err != nil || len(repos) != 1 {
		t.Fatalf("expected one SSH source repo, repos=%v err=%v", repos, err)
	}
	nodes, err := env.Store.ListRemoteNodesByUser(context.Background(), env.UserID)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("expected one SSH source node, nodes=%v err=%v", nodes, err)
	}

	secondCredentialID := createCredential("rotated", "PRIVATE-KEY-TWO")
	rotated := env.doRequest(http.MethodPost, "/api/scans", sourceBody(secondCredentialID))
	if rotated.Code != http.StatusCreated {
		t.Fatalf("rotate source credential: %d %s", rotated.Code, rotated.Body.String())
	}
	repos, _ = env.Store.ListReposByUser(context.Background(), env.UserID)
	nodes, _ = env.Store.ListRemoteNodesByUser(context.Background(), env.UserID)
	if len(repos) != 1 || len(nodes) != 1 || nodes[0].CredentialSecretID == nil ||
		*nodes[0].CredentialSecretID != secondCredentialID {
		t.Fatalf("SSH upsert did not converge or rotate credential: repos=%d nodes=%#v",
			len(repos), nodes)
	}
}

func TestCreateScanRejectsAmbiguousSourceAndTraversalScope(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)

	ambiguous := env.doRequest(http.MethodPost, "/api/scans", map[string]interface{}{
		"repo_id": repoID,
		"source":  map[string]string{"kind": "git", "url": "https://example.com/repo.git"},
	})
	if ambiguous.Code != http.StatusBadRequest {
		t.Fatalf("expected ambiguous-source 400, got %d: %s", ambiguous.Code, ambiguous.Body.String())
	}
	traversal := env.doRequest(http.MethodPost, "/api/scans", map[string]interface{}{
		"repo_id": repoID, "include_paths": []string{"../private/**"},
	})
	if traversal.Code != http.StatusBadRequest {
		t.Fatalf("expected traversal 400, got %d: %s", traversal.Code, traversal.Body.String())
	}
	windowsAbsolute := env.doRequest(http.MethodPost, "/api/scans", map[string]interface{}{
		"repo_id": repoID, "include_paths": []string{`C:\private\**`},
	})
	if windowsAbsolute.Code != http.StatusBadRequest {
		t.Fatalf("expected Windows absolute path 400, got %d: %s", windowsAbsolute.Code, windowsAbsolute.Body.String())
	}
}

func TestCreateScanNonexistentRepo(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodPost, "/api/scans", map[string]string{
		"repo_id": "nonexistent",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateScanUnauthorized(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/scans", bytes.NewReader([]byte(`{"repo_id":"x"}`)))
	w := httptest.NewRecorder()
	env.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestListScans(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	// Create two scans
	env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})
	env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})

	w := env.doRequest(http.MethodGet, "/api/scans", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 2 {
		t.Errorf("expected 2 scans, got %d", resp.Meta.Total)
	}
}

func TestListScansFilterByRepoID(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})

	w := env.doRequest(http.MethodGet, "/api/scans?repo_id="+repoID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w = env.doRequest(http.MethodGet, "/api/scans?repo_id=nonexistent", nil)
	var resp struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 0 {
		t.Errorf("expected 0 scans for non-matching repo_id, got %d", resp.Meta.Total)
	}
}

func TestListScansFilterByStatus(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	// Seed scans with explicit statuses directly. Going through POST
	// /api/scans would spawn the async executeScan goroutine, which both
	// transitions the scan pending→running (racing a status-filter assert)
	// and can write to the store after the deferred Close() — surfacing as a
	// flaky "sql: database is closed" that bleeds into sibling tests.
	mkScan := func(status models.ScanStatus) {
		if err := env.Store.CreateScan(context.Background(), &models.Scan{
			ID:              uuid.New().String(),
			UserID:          env.UserID,
			RepoID:          repoID,
			Branch:          "main",
			Status:          status,
			ToolsSelected:   "[]",
			ToolsCompleted:  "[]",
			ToolsFailed:     "[]",
			CoverageSummary: "{}",
		}); err != nil {
			t.Fatalf("CreateScan(%s): %v", status, err)
		}
	}
	mkScan(models.ScanStatusPending)
	mkScan(models.ScanStatusCompleted)

	assertTotal := func(status string, want int) {
		t.Helper()
		w := env.doRequest(http.MethodGet, "/api/scans?status="+status, nil)
		var resp struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode status=%s: %v", status, err)
		}
		if resp.Meta.Total != want {
			t.Errorf("status=%s: expected %d scan(s), got %d", status, want, resp.Meta.Total)
		}
	}
	assertTotal("pending", 1)
	assertTotal("completed", 1)
	assertTotal("running", 0)
}

func TestGetScan(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	w := env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})

	var createResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)

	w = env.doRequest(http.MethodGet, "/api/scans/"+createResp.Data.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetScanNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/scans/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCancelScan(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	w := env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})
	var createResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)

	w = env.doRequest(http.MethodDelete, "/api/scans/"+createResp.Data.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cancelResp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &cancelResp)
	if cancelResp.Data.Status != "cancelled" {
		t.Errorf("expected status cancelled, got %s", cancelResp.Data.Status)
	}
}

func TestCancelScanAlreadyCompleted(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	// Create and then manually complete a scan
	now := time.Now()
	scan := &models.Scan{
		ID:        uuid.New().String(),
		UserID:    env.UserID,
		RepoID:    repoID,
		Status:    models.ScanStatusCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	w := env.doRequest(http.MethodDelete, "/api/scans/"+scan.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCancelScanNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodDelete, "/api/scans/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Scan Findings tests ---

func TestGetScanFindings(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID:        uuid.New().String(),
		UserID:    env.UserID,
		RepoID:    repoID,
		Status:    models.ScanStatusCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	// Add findings
	findings := []models.Finding{
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityHigh, Title: "SQL Injection", ToolName: "semgrep", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityLow, Title: "Unused var", ToolName: "gosec", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityCritical, Title: "RCE", ToolName: "semgrep", Status: models.StatusFixed, CreatedAt: now, UpdatedAt: now},
	}
	env.Store.CreateFindings(context.Background(), findings)

	// List all
	w := env.doRequest(http.MethodGet, fmt.Sprintf("/api/scans/%s/findings", scan.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 3 {
		t.Errorf("expected 3 findings, got %d", resp.Meta.Total)
	}

	// Filter by severity
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/scans/%s/findings?severity=high", scan.ID), nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 high finding, got %d", resp.Meta.Total)
	}

	// Filter by tool
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/scans/%s/findings?tool=semgrep", scan.ID), nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 2 {
		t.Errorf("expected 2 semgrep findings, got %d", resp.Meta.Total)
	}

	// Filter by status
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/scans/%s/findings?status=open", scan.ID), nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 2 {
		t.Errorf("expected 2 open findings, got %d", resp.Meta.Total)
	}
}

func TestGetScanFindingsSorted(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	findings := []models.Finding{
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityLow, CompositeScore: 2.0, Title: "Low", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityHigh, CompositeScore: 8.0, Title: "High", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
	}
	env.Store.CreateFindings(context.Background(), findings)

	w := env.doRequest(http.MethodGet, fmt.Sprintf("/api/scans/%s/findings?sort=composite_score&order=desc", scan.ID), nil)
	var resp struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) == 2 && resp.Data[0].Title != "High" {
		t.Errorf("expected High first when sorted desc, got %s", resp.Data[0].Title)
	}
}

func TestGetScanFindingsNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/scans/nonexistent/findings", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Findings tests ---

func TestGetFinding(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	findingID := uuid.New().String()
	env.Store.CreateFinding(context.Background(), &models.Finding{
		ID: findingID, ScanID: scan.ID, RepoID: repoID,
		Severity: models.SeverityHigh, Title: "Test Finding",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodGet, "/api/findings/"+findingID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetFindingNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/findings/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpdateFindingStatus(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	findingID := uuid.New().String()
	env.Store.CreateFinding(context.Background(), &models.Finding{
		ID: findingID, ScanID: scan.ID, RepoID: repoID,
		Severity: models.SeverityHigh, Title: "Test",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	})

	tests := []struct {
		name     string
		status   string
		wantCode int
	}{
		{name: "mark wont_fix", status: "wont_fix", wantCode: http.StatusOK},
		{name: "mark false_positive", status: "false_positive", wantCode: http.StatusOK},
		{name: "mark open", status: "open", wantCode: http.StatusOK},
		{name: "invalid status", status: "invalid", wantCode: http.StatusBadRequest},
		{name: "fixed not allowed manually", status: "fixed", wantCode: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := env.doRequest(http.MethodPut, "/api/findings/"+findingID+"/status",
				map[string]string{"status": tt.status})
			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestUpdateFindingStatusNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodPut, "/api/findings/nonexistent/status",
		map[string]string{"status": "wont_fix"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListFindings(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	env.Store.CreateFindings(context.Background(), []models.Finding{
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityHigh, Category: models.CategorySAST, Title: "A", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityLow, Category: models.CategorySCA, Title: "B", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
	})

	w := env.doRequest(http.MethodGet, "/api/findings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 2 {
		t.Errorf("expected 2 findings, got %d", resp.Meta.Total)
	}

	// Filter by severity
	w = env.doRequest(http.MethodGet, "/api/findings?severity=high", nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 high finding, got %d", resp.Meta.Total)
	}

	// Filter by category
	w = env.doRequest(http.MethodGet, "/api/findings?category=sca", nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 sca finding, got %d", resp.Meta.Total)
	}
}

func TestFindingTrends(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	env.Store.CreateFindings(context.Background(), []models.Finding{
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityHigh, Title: "A", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID, Severity: models.SeverityCritical, Title: "B", Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now},
	})

	w := env.doRequest(http.MethodGet, "/api/findings/trends", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data []struct {
			Date   string `json:"date"`
			Counts struct {
				Total    int `json:"total"`
				Critical int `json:"critical"`
				High     int `json:"high"`
			} `json:"counts"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) == 0 {
		t.Fatal("expected at least one trend entry")
	}
	if resp.Data[0].Counts.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Data[0].Counts.Total)
	}
}

// --- Fix tests (autonomous fix engine) ---
//
// The /fixes execute path is gated by the autofix_enabled setting (default
// off). With it off, POST /fixes returns 403 autofix_disabled. With it on,
// POST enqueues a durable fix job (no real fixing happens here — the worker
// claims and runs it out-of-process).

func (e *testEnv) enableAutofix(t *testing.T) {
	t.Helper()
	if err := e.Store.SetSetting(context.Background(), "autofix_enabled", "true"); err != nil {
		t.Fatalf("enable autofix: %v", err)
	}
}

func (e *testEnv) createScan(t *testing.T, repoID string) string {
	t.Helper()
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(context.Background(), scan); err != nil {
		t.Fatalf("create scan: %v", err)
	}
	return scan.ID
}

func TestCreateFixFlagOffReturns403(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	w := env.doRequest(http.MethodPost, "/api/fixes", map[string]interface{}{
		"repo_id": repoID,
		"scan_id": scanID,
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("flag off: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "autofix_disabled") {
		t.Errorf("expected autofix_disabled error code, got %s", w.Body.String())
	}
}

func TestCreateFixEnqueuesJob(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	w := env.doRequest(http.MethodPost, "/api/fixes", map[string]interface{}{
		"repo_id":        repoID,
		"scan_id":        scanID,
		"severity_floor": "high",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Mode   string `json:"mode"`
			Engine string `json:"engine"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Status != models.FixJobQueued {
		t.Errorf("expected queued status, got %s", resp.Data.Status)
	}
	if resp.Data.Mode != models.FixModeDryRun {
		t.Errorf("expected dry_run mode default, got %s", resp.Data.Mode)
	}
	if resp.Data.Engine != "auto" {
		t.Errorf("expected auto engine default, got %s", resp.Data.Engine)
	}

	// The job is durable: it exists in the queue.
	job, err := env.Store.GetFixJobByID(context.Background(), resp.Data.ID)
	if err != nil || job == nil {
		t.Fatalf("enqueued job not found in store: %v", err)
	}
	if job.RepoID != repoID {
		t.Errorf("job repo_id = %s, want %s", job.RepoID, repoID)
	}
}

func TestCreateFixMissingRepoID(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)

	w := env.doRequest(http.MethodPost, "/api/fixes", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestListFixes(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	now := time.Now().UTC()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: uuid.New().String(), UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobQueued, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fixes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 job, got %d", resp.Meta.Total)
	}
}

func TestQueuedBehindOnListAndGet(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	runningID := uuid.New().String()
	queuedID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: runningID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobQueued, Mode: models.FixModeDryRun,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue running: %v", err)
	}
	runJob, err := env.Store.GetFixJobByID(context.Background(), runningID)
	if err != nil || runJob == nil {
		t.Fatalf("load: %v", err)
	}
	runJob.Status = models.FixJobRunning
	started := now.Add(-30 * time.Second)
	runJob.StartedAt = &started
	if err := env.Store.UpdateFixJob(context.Background(), runJob); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: queuedID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobQueued, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue queued: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fixes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list %d: %s", w.Code, w.Body.String())
	}
	var list struct {
		Data []struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			QueuedBehind *struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"queued_behind"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, j := range list.Data {
		if j.ID == queuedID {
			found = true
			if j.QueuedBehind == nil || j.QueuedBehind.ID != runningID || j.QueuedBehind.Kind != "job" {
				t.Fatalf("queued_behind = %+v, want job %s", j.QueuedBehind, runningID)
			}
		}
		if j.ID == runningID && j.QueuedBehind != nil {
			t.Fatal("running job should not have queued_behind")
		}
	}
	if !found {
		t.Fatal("queued job missing from list")
	}

	w = env.doRequest(http.MethodGet, "/api/fixes/"+queuedID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get %d: %s", w.Code, w.Body.String())
	}
	var detail struct {
		Data struct {
			QueuedBehind *struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"queued_behind"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Data.QueuedBehind == nil || detail.Data.QueuedBehind.ID != runningID {
		t.Fatalf("get queued_behind = %+v", detail.Data.QueuedBehind)
	}
}

func TestGetFixWithAttempts(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobSucceeded, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := env.Store.CreateFixAttempt(context.Background(), &models.FixAttempt{
		ID: uuid.New().String(), JobID: jobID, FindingID: "f1", AttemptNo: 1,
		EngineUsed: "api", Outcome: models.FixOutcomeKept, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create attempt: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fixes/"+jobID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			ID       string `json:"id"`
			Attempts []struct {
				Outcome string `json:"outcome"`
			} `json:"attempts"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data.Attempts) != 1 || resp.Data.Attempts[0].Outcome != models.FixOutcomeKept {
		t.Errorf("expected one kept attempt, got %+v", resp.Data.Attempts)
	}
}

func TestGetFixNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/fixes/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetFixDiff(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	// Point the artifact store at a temp dir and seed a diff for the job.
	root := t.TempDir()
	if err := artifacts.Init(root); err != nil {
		t.Fatalf("artifacts init: %v", err)
	}

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobSucceeded, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	want := "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-bad\n+good\n"
	fs := fixstore.New(root)
	if _, err := fs.SaveDiff(context.Background(), jobID, want); err != nil {
		t.Fatalf("save diff: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fixes/"+jobID+"/diff", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != want {
		t.Errorf("diff body mismatch:\n got %q\nwant %q", w.Body.String(), want)
	}
}

func TestGetFixDiffNotYetAvailable(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	root := t.TempDir()
	if err := artifacts.Init(root); err != nil {
		t.Fatalf("artifacts init: %v", err)
	}

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobRunning, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fixes/"+jobID+"/diff", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing diff, got %d", w.Code)
	}
}

func TestCancelFix(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobQueued, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := env.doRequest(http.MethodDelete, "/api/fixes/"+jobID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Status != models.FixJobCancelled {
		t.Errorf("expected cancelled, got %s", resp.Data.Status)
	}
}

func TestCancelFixAwaitingPushDiscardsBranch(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobAwaitingPush, Mode: models.FixModeDryRun,
		ResultBranch: "wolf-fix/" + jobID, PauseReason: "verified branch is ready to push for review",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := env.doRequest(http.MethodDelete, "/api/fixes/"+jobID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Status       string `json:"status"`
			ResultBranch string `json:"result_branch"`
			PauseReason  string `json:"pause_reason"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Status != models.FixJobCancelled {
		t.Errorf("expected cancelled, got %s", resp.Data.Status)
	}
	if resp.Data.ResultBranch != "" {
		t.Errorf("expected result_branch cleared, got %q", resp.Data.ResultBranch)
	}
	if resp.Data.PauseReason != "" {
		t.Errorf("expected pause_reason cleared, got %q", resp.Data.PauseReason)
	}
}

func TestCancelFixAlreadyFinished(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now().UTC()
	jobID := uuid.New().String()
	if err := env.Store.EnqueueFixJob(context.Background(), &models.FixJob{
		ID: jobID, UserID: env.UserID, Type: "fix", RepoID: repoID,
		Status: models.FixJobSucceeded, Mode: models.FixModeDryRun,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := env.doRequest(http.MethodDelete, "/api/fixes/"+jobID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// --- Pagination/helper tests ---

func TestPaginationDefaults(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/scans?page=abc&per_page=-1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
