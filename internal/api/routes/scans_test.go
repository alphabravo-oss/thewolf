package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
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
		// Scans
		r.Post("/api/scans", routes.CreateScan)
		r.Get("/api/scans", routes.ListScans)
		r.Get("/api/scans/{id}", routes.GetScan)
		r.Get("/api/scans/{id}/findings", routes.GetScanFindings)
		r.Delete("/api/scans/{id}", routes.CancelScan)
		// Findings
		r.Get("/api/findings", routes.ListFindings)
		r.Get("/api/findings/{id}", routes.GetFinding)
		r.Put("/api/findings/{id}/status", routes.UpdateFindingStatus)
		r.Get("/api/findings/trends", routes.FindingTrends)
		// Fixes
		r.Post("/api/fixes", routes.CreateFix)
		r.Get("/api/fixes", routes.ListFixes)
		r.Get("/api/fixes/{id}", routes.GetFix)
		r.Delete("/api/fixes/{id}", routes.CancelFix)
		// Loops
		r.Get("/api/loops", routes.ListLoops)
		r.Get("/api/loops/{id}", routes.GetLoop)
	})

	// Register user
	body, _ := json.Marshal(map[string]string{
		"email":    "testuser@example.com",
		"password": "password123",
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
	env.doRequest(http.MethodPost, "/api/scans", map[string]string{"repo_id": repoID})

	w := env.doRequest(http.MethodGet, "/api/scans?status=pending", nil)
	var resp struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 pending scan, got %d", resp.Meta.Total)
	}

	w = env.doRequest(http.MethodGet, "/api/scans?status=completed", nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 0 {
		t.Errorf("expected 0 completed scans, got %d", resp.Meta.Total)
	}
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
		name       string
		status     string
		wantCode   int
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

// --- Fix tests ---

func TestCreateFix(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	env.Store.CreateScan(context.Background(), scan)

	// Ensure the test repo path exists and is a git repo
	os.MkdirAll("/tmp/test-repo", 0755)
	exec.Command("git", "init", "/tmp/test-repo").CombinedOutput()
	exec.Command("git", "-C", "/tmp/test-repo", "commit", "--allow-empty", "-m", "init").CombinedOutput()

	w := env.doRequest(http.MethodPost, "/api/fixes", map[string]interface{}{
		"scan_id":  scan.ID,
		"severity": []string{"high", "critical"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Status != "pending" {
		t.Errorf("expected pending status, got %s", resp.Data.Status)
	}
}

func TestCreateFixMissingRequired(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodPost, "/api/fixes", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
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

func TestListFixes(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	now := time.Now()
	env.Store.CreateFix(context.Background(), &models.Fix{
		ID: uuid.New().String(), UserID: env.UserID, ScanID: scanID,
		Status: models.FixStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodGet, "/api/fixes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetFix(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	now := time.Now()
	fixID := uuid.New().String()
	env.Store.CreateFix(context.Background(), &models.Fix{
		ID: fixID, UserID: env.UserID, ScanID: scanID,
		Status: models.FixStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodGet, "/api/fixes/"+fixID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
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

func TestCancelFix(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	now := time.Now()
	fixID := uuid.New().String()
	env.Store.CreateFix(context.Background(), &models.Fix{
		ID: fixID, UserID: env.UserID, ScanID: scanID,
		Status: models.FixStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodDelete, "/api/fixes/"+fixID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Status != "cancelled" {
		t.Errorf("expected cancelled, got %s", resp.Data.Status)
	}
}

func TestCancelFixAlreadyCompleted(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	now := time.Now()
	fixID := uuid.New().String()
	env.Store.CreateFix(context.Background(), &models.Fix{
		ID: fixID, UserID: env.UserID, ScanID: scanID,
		Status: models.FixStatusCompleted, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodDelete, "/api/fixes/"+fixID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// --- Loop tests ---

func TestListLoops(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	now := time.Now()
	env.Store.CreateLoop(context.Background(), &models.Loop{
		ID: uuid.New().String(), UserID: env.UserID, RepoID: repoID,
		Status: models.LoopStatusRunning, CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodGet, "/api/loops", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Meta.Total != 1 {
		t.Errorf("expected 1 loop, got %d", resp.Meta.Total)
	}
}

func TestGetLoop(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)

	now := time.Now()
	loopID := uuid.New().String()
	env.Store.CreateLoop(context.Background(), &models.Loop{
		ID: loopID, UserID: env.UserID, RepoID: repoID,
		Status: models.LoopStatusRunning, MaxIterations: 5,
		CreatedAt: now, UpdatedAt: now,
	})

	w := env.doRequest(http.MethodGet, "/api/loops/"+loopID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetLoopNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/loops/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
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
