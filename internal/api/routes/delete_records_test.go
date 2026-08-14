package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestDeleteRepoKeepsRecordsUnlessPurged(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	repoID := e.createRepo(t)
	scanID := uuid.New().String()
	if err := e.Store.CreateScan(ctx, &models.Scan{
		ID: scanID, UserID: e.UserID, RepoID: repoID, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("create scan: %v", err)
	}

	w := e.doRequest(http.MethodDelete, "/api/repos/"+repoID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete without purge: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := e.Store.GetRepoByID(ctx, repoID); err != nil {
		t.Fatalf("repo should still exist: %v", err)
	}
	if _, err := e.Store.GetScanByID(ctx, scanID); err != nil {
		t.Fatalf("scan should still exist: %v", err)
	}

	w = e.doRequest(http.MethodDelete, "/api/repos/"+repoID+"?purge=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete with purge: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := e.Store.GetRepoByID(ctx, repoID); err == nil {
		t.Fatal("repo should be gone")
	}
}

func TestDeleteCollectionKeepsScanHistoryByDefault(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()

	w := e.doRequest(http.MethodPost, "/api/collections", map[string]string{"name": "keep-history"})
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create collection: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data models.Collection `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode collection: %v %s", err, w.Body.String())
	}
	colID := created.Data.ID
	if colID == "" {
		t.Fatalf("missing collection id: %s", w.Body.String())
	}

	repoID := e.createRepo(t)
	_ = e.Store.AddRepoToCollection(ctx, colID, repoID)
	scanID := uuid.New().String()
	if err := e.Store.CreateScan(ctx, &models.Scan{
		ID: scanID, UserID: e.UserID, RepoID: repoID, CollectionID: &colID, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("create scan: %v", err)
	}

	w = e.doRequest(http.MethodDelete, "/api/collections/"+colID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete collection: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := e.Store.GetCollectionByID(ctx, colID); err == nil {
		t.Fatal("collection should be gone")
	}
	scan, err := e.Store.GetScanByID(ctx, scanID)
	if err != nil {
		t.Fatalf("scan should remain: %v", err)
	}
	if scan.CollectionID != nil && *scan.CollectionID != "" {
		t.Fatalf("scan collection_id should be cleared, got %v", scan.CollectionID)
	}

	col2 := &models.Collection{ID: uuid.New().String(), UserID: e.UserID, Name: "purge-me"}
	if err := e.Store.CreateCollection(ctx, col2); err != nil {
		t.Fatalf("create col2: %v", err)
	}
	scan2 := uuid.New().String()
	if err := e.Store.CreateScan(ctx, &models.Scan{
		ID: scan2, UserID: e.UserID, RepoID: repoID, CollectionID: &col2.ID, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("create scan2: %v", err)
	}
	w = e.doRequest(http.MethodDelete, "/api/collections/"+col2.ID+"?purge=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("purge collection: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := e.Store.GetScanByID(ctx, scan2); err == nil {
		t.Fatal("purged scan should be gone")
	}
}

func TestPurgeOrphanScansAPI(t *testing.T) {
	e := setupTestEnv(t)
	w := e.doRequest(http.MethodGet, "/api/scans/orphans", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list orphans: %d %s", w.Code, w.Body.String())
	}
	w = e.doRequest(http.MethodDelete, "/api/scans/orphans", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("purge orphans: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Purged int `json:"purged"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Purged != 0 {
		t.Fatalf("expected no orphans, purged=%d", resp.Data.Purged)
	}
}
