package routes_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// TestFindingsAreTenantScoped is the regression guard for the IDOR on
// GET /api/findings/{id} and PUT /api/findings/{id}/status: a caller must
// never read or mutate another tenant's finding. Cross-tenant misses 404
// (not 403) so we don't confirm the finding exists.
func TestFindingsAreTenantScoped(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()

	// setupTestEnv's first user is the bootstrap admin (fleetVisible). Demote
	// so this exercises the owner check, not the admin read path.
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatalf("demote caller: %v", err)
	}

	mineRepo := e.createRepo(t)
	now := time.Now()
	mineScan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: mineRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, mineScan); err != nil {
		t.Fatalf("CreateScan mine: %v", err)
	}
	mineID := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: mineID, ScanID: mineScan.ID, RepoID: mineRepo,
		Severity: models.SeverityHigh, Title: "mine",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding mine: %v", err)
	}

	otherID := uuid.New().String()
	if err := e.Store.CreateUser(ctx, &models.User{
		ID: otherID, Email: "other-finding@example.com", PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	theirsRepo := uuid.New().String()
	if err := e.Store.CreateRepo(ctx, &models.Repo{
		ID: theirsRepo, UserID: otherID, Name: "theirs",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/theirs", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo theirs: %v", err)
	}
	theirsScan := &models.Scan{
		ID: uuid.New().String(), UserID: otherID, RepoID: theirsRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, theirsScan); err != nil {
		t.Fatalf("CreateScan theirs: %v", err)
	}
	theirsID := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: theirsID, ScanID: theirsScan.ID, RepoID: theirsRepo,
		Severity: models.SeverityHigh, Title: "theirs",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding theirs: %v", err)
	}

	if w := e.doRequest(http.MethodGet, "/api/findings/"+theirsID, nil); w.Code != http.StatusNotFound {
		t.Errorf("GET another user's finding: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if w := e.doRequest(http.MethodPut, "/api/findings/"+theirsID+"/status",
		map[string]string{"status": "wont_fix"}); w.Code != http.StatusNotFound {
		t.Errorf("PUT status on another user's finding: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if w := e.doRequest(http.MethodGet, "/api/findings/"+mineID, nil); w.Code != http.StatusOK {
		t.Errorf("owner reading own finding: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBulkUpdateFindingsStatus(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatalf("demote caller: %v", err)
	}

	repoID := e.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	id := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: id, ScanID: scan.ID, RepoID: repoID,
		Severity: models.SeverityHigh, Title: "mine",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	w := e.doRequest(http.MethodPost, "/api/findings/bulk", map[string]any{
		"ids": []string{id}, "status": "wont_fix",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bulk status: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated, err := e.Store.GetFindingByID(ctx, id)
	if err != nil {
		t.Fatalf("GetFindingByID: %v", err)
	}
	if updated.Status != models.StatusWontFix {
		t.Fatalf("status = %q, want wont_fix", updated.Status)
	}
}

func TestBulkUpdateFindingsIDOR(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatalf("demote caller: %v", err)
	}

	now := time.Now()
	mineRepo := e.createRepo(t)
	mineScan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: mineRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, mineScan); err != nil {
		t.Fatalf("CreateScan mine: %v", err)
	}
	mineID := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: mineID, ScanID: mineScan.ID, RepoID: mineRepo,
		Severity: models.SeverityHigh, Title: "mine",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding mine: %v", err)
	}

	otherID := uuid.New().String()
	if err := e.Store.CreateUser(ctx, &models.User{
		ID: otherID, Email: "other-bulk@example.com", PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	theirsRepo := uuid.New().String()
	if err := e.Store.CreateRepo(ctx, &models.Repo{
		ID: theirsRepo, UserID: otherID, Name: "theirs",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/theirs-bulk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo theirs: %v", err)
	}
	theirsScan := &models.Scan{
		ID: uuid.New().String(), UserID: otherID, RepoID: theirsRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, theirsScan); err != nil {
		t.Fatalf("CreateScan theirs: %v", err)
	}
	theirsID := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: theirsID, ScanID: theirsScan.ID, RepoID: theirsRepo,
		Severity: models.SeverityHigh, Title: "theirs",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding theirs: %v", err)
	}

	w := e.doRequest(http.MethodPost, "/api/findings/bulk", map[string]any{
		"ids": []string{theirsID}, "status": "wont_fix",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("bulk other user's finding: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	theirs, err := e.Store.GetFindingByID(ctx, theirsID)
	if err != nil {
		t.Fatalf("GetFindingByID: %v", err)
	}
	if theirs.Status != models.StatusOpen {
		t.Fatalf("other user's finding mutated: %q", theirs.Status)
	}
}
