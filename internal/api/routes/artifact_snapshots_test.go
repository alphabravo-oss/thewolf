package routes_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scan/report"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestGateArtifactSnapshotsPreserveRecordedBytes(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: e.UserID, Status: models.ScanStatusCompleted, ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	first := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/gate", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first gate: %d %s", first.Code, first.Body.String())
	}
	rows, err := e.Store.ListScanArtifacts(ctx, scan.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("first artifact: %+v %v", rows, err)
	}
	before, err := artifacts.ReadFile(rows[0].FilePath)
	if err != nil {
		t.Fatal(err)
	}
	second := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/gate", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second gate: %d %s", second.Code, second.Body.String())
	}
	after, err := artifacts.ReadFile(rows[0].FilePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(after)
	if !bytes.Equal(before, after) || hex.EncodeToString(digest[:]) != rows[0].ChecksumSHA256 {
		t.Fatalf("recorded artifact was overwritten: path=%s expected_checksum=%s actual_checksum=%x", rows[0].FilePath, rows[0].ChecksumSHA256, digest)
	}
}

type artifactFailureStore struct {
	db.Store
	comparison bool
}

func (s artifactFailureStore) CreateScanArtifact(context.Context, *models.ScanArtifact) error {
	return errors.New("fixture artifact failure")
}
func (s artifactFailureStore) UpsertScanComparison(ctx context.Context, c *models.ScanComparison) error {
	if s.comparison {
		return errors.New("fixture comparison failure")
	}
	return s.Store.UpsertScanComparison(ctx, c)
}

func TestArtifactSnapshotDownloadsAndFailures(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	scans := []*models.Scan{}
	for range 2 {
		scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: e.UserID, Status: models.ScanStatusCompleted, ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`}
		if err := e.Store.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		scans = append(scans, scan)
	}
	scan := scans[1]
	base := "/api/scans/" + scan.ID
	compare := "/api/scans/" + scans[0].ID + "/compare/" + scan.ID
	var wg sync.WaitGroup
	for range 6 {
		for _, url := range []string{base + "/gate", compare} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := e.doRequest(http.MethodGet, url, nil)
				if w.Code != http.StatusOK {
					t.Errorf("publish %s: %d %s", url, w.Code, w.Body.String())
				}
			}()
		}
	}
	wg.Wait()
	rows, err := e.Store.ListScanArtifacts(ctx, scan.ID)
	if err != nil || len(rows) != 12 {
		t.Fatalf("history: %d %v", len(rows), err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.StorageKey] || row.FindingsRevision == nil || *row.FindingsRevision != 0 {
			t.Fatalf("bad snapshot: %+v", row)
		}
		seen[row.StorageKey] = true
		w := e.doRequest(http.MethodGet, base+"/artifacts/"+row.ID+"/download", nil)
		digest := sha256.Sum256(w.Body.Bytes())
		if w.Code != http.StatusOK || hex.EncodeToString(digest[:]) != row.ChecksumSHA256 || w.Header().Get("X-Wolf-Artifact-Integrity") != "verified" || w.Header().Get("X-Wolf-Findings-Revision") != "0" {
			t.Fatalf("download mismatch: %d %s", w.Code, w.Body.String())
		}
		w = e.doRequest(http.MethodGet, "/api/scans/"+scans[0].ID+"/artifacts/"+row.ID+"/download", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("cross-scan artifact returned: %d", w.Code)
		}
	}
	row := rows[0]
	if err := os.WriteFile(row.FilePath, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodGet, base+"/artifacts/"+row.ID+"/download", nil)
	if w.Code != http.StatusConflict || !bytes.Contains(w.Body.Bytes(), []byte("artifact_integrity")) || bytes.Contains(w.Body.Bytes(), []byte("corrupted")) {
		t.Fatalf("corrupt download: %d %s", w.Code, w.Body.String())
	}
	// Both HTTP publishers must propagate metadata persistence failure.
	routes.DefaultHandler.Store = artifactFailureStore{Store: e.Store}
	for _, url := range []string{base + "/gate", compare} {
		w := e.doRequest(http.MethodGet, url, nil)
		if w.Code != http.StatusInternalServerError || !bytes.Contains(w.Body.Bytes(), []byte("artifact_error")) {
			t.Fatalf("hidden publication error: %d %s", w.Code, w.Body.String())
		}
	}
	routes.DefaultHandler.Store = artifactFailureStore{Store: e.Store, comparison: true}
	w = e.doRequest(http.MethodGet, compare, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("hidden comparison error: %d %s", w.Code, w.Body.String())
	}
	routes.DefaultHandler.Store = e.Store
	after, err := e.Store.ListScanArtifacts(ctx, scan.ID)
	if err != nil || len(after) != len(rows) {
		t.Fatalf("failed publication recorded: %d %v", len(after), err)
	}
}

func TestManifestUsesCurrentRevisionAndPreservesSavedDownload(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: e.UserID, Status: models.ScanStatusCompleted}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	revision := int64(0)
	payload, err := report.MarshalManifest(report.Manifest{ScanID: scan.ID, FindingsRevision: &revision})
	if err != nil {
		t.Fatal(err)
	}
	record, err := artifacts.Publish(ctx, scan.ID, "manifest.json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	record.ArtifactType = models.ArtifactManifest
	record.FindingsRevision = &revision
	if err := e.Store.CreateScanArtifact(ctx, record); err != nil {
		t.Fatal(err)
	}
	base := "/api/scans/" + scan.ID
	first := e.doRequest(http.MethodGet, base+"/manifest", nil)
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), payload) {
		t.Fatalf("saved manifest: %d %s", first.Code, first.Body.String())
	}
	finding := &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "a", Severity: models.SeverityHigh, Title: "issue", FilePath: "app.go", Status: models.StatusOpen}
	if err := e.Store.CreateFinding(ctx, finding); err != nil {
		t.Fatal(err)
	}
	current := e.doRequest(http.MethodGet, base+"/manifest", nil)
	var manifest report.Manifest
	if err := json.Unmarshal(current.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if current.Code != http.StatusOK || manifest.FindingsRevision == nil || *manifest.FindingsRevision != 1 || manifest.Counts.Visible != 1 || current.Header().Get("X-Wolf-Findings-Revision") != strconv.Itoa(1) {
		t.Fatalf("stale live manifest: %d %s", current.Code, current.Body.String())
	}
	saved := e.doRequest(http.MethodGet, base+"/artifacts/"+record.ID+"/download", nil)
	if saved.Code != http.StatusOK || !bytes.Equal(saved.Body.Bytes(), payload) || saved.Header().Get("X-Wolf-Findings-Revision") != "0" {
		t.Fatalf("historical manifest changed: %d %s", saved.Code, saved.Body.String())
	}
	if err := os.WriteFile(record.FilePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := e.doRequest(http.MethodGet, base+"/manifest", nil)
	if corrupt.Code != http.StatusConflict {
		t.Fatalf("manifest corruption hidden: %d %s", corrupt.Code, corrupt.Body.String())
	}
}
