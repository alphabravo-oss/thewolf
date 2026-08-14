package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestCreateFixAttachesSameRemediationAndBranch(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	w1 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID, "human_in_the_loop": false,
	})
	if w1.Code != http.StatusCreated {
		t.Fatalf("first enqueue %d: %s", w1.Code, w1.Body.String())
	}
	var first struct {
		Data struct {
			ID            string `json:"id"`
			TargetBranch  string `json:"target_branch"`
			RemediationID string `json:"remediation_id"`
			WorkspacePath string `json:"workspace_path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	wantBranch := "wolf-fix/" + scanID
	if first.Data.TargetBranch != wantBranch {
		t.Fatalf("branch=%q want %q", first.Data.TargetBranch, wantBranch)
	}
	if first.Data.RemediationID == "" {
		t.Fatal("expected remediation_id")
	}

	// Simulate the first job having prepared a workspace and still running.
	job, err := env.Store.GetFixJobByID(context.Background(), first.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = models.FixJobRunning
	job.WorkspacePath = "/workspaces/shared"
	if err := env.Store.UpdateFixJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	rem, err := env.Store.GetRemediationByID(context.Background(), first.Data.RemediationID)
	if err != nil || rem == nil {
		t.Fatalf("rem: %v", err)
	}
	rem.WorkspacePath = "/workspaces/shared"
	if err := env.Store.UpdateRemediation(context.Background(), rem); err != nil {
		t.Fatal(err)
	}

	wBusy := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if wBusy.Code != http.StatusConflict {
		t.Fatalf("busy enqueue %d: %s", wBusy.Code, wBusy.Body.String())
	}

	job.Status = models.FixJobSucceeded
	if err := env.Store.UpdateFixJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	w2 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if w2.Code != http.StatusCreated {
		t.Fatalf("second enqueue %d: %s", w2.Code, w2.Body.String())
	}
	var second struct {
		Data struct {
			ID            string `json:"id"`
			TargetBranch  string `json:"target_branch"`
			RemediationID string `json:"remediation_id"`
			WorkspacePath string `json:"workspace_path"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &second)
	if second.Data.ID == first.Data.ID {
		t.Fatal("second job should be a new agent")
	}
	if second.Data.RemediationID != first.Data.RemediationID {
		t.Fatalf("remediation %q vs %q", second.Data.RemediationID, first.Data.RemediationID)
	}
	if second.Data.TargetBranch != wantBranch {
		t.Fatalf("second branch=%q", second.Data.TargetBranch)
	}
	if second.Data.WorkspacePath != "/workspaces/shared" {
		t.Fatalf("workspace=%q", second.Data.WorkspacePath)
	}
}

func TestCreateFixFrozenOriginConflict(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)

	w1 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if w1.Code != http.StatusCreated {
		t.Fatalf("enqueue %d: %s", w1.Code, w1.Body.String())
	}
	var first struct {
		Data struct {
			RemediationID string `json:"remediation_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &first)
	rem, _ := env.Store.GetRemediationByID(context.Background(), first.Data.RemediationID)
	rem.State = models.RemediationFrozen
	if err := env.Store.UpdateRemediation(context.Background(), rem); err != nil {
		t.Fatal(err)
	}

	w2 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if w2.Code != http.StatusConflict {
		t.Fatalf("frozen enqueue %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "remediation_frozen") {
		t.Fatalf("body=%s", w2.Body.String())
	}
}

func TestCancelFixDiscardsRemediation(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)
	w1 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	var first struct {
		Data struct {
			ID            string `json:"id"`
			RemediationID string `json:"remediation_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &first)

	wDel := env.doRequest(http.MethodDelete, "/api/fixes/"+first.Data.ID, nil)
	if wDel.Code != http.StatusOK {
		t.Fatalf("cancel %d: %s", wDel.Code, wDel.Body.String())
	}
	rem, _ := env.Store.GetRemediationByID(context.Background(), first.Data.RemediationID)
	if rem == nil || rem.State != models.RemediationDiscarded {
		t.Fatalf("expected discarded rem, got %+v", rem)
	}

	// Next Fix starts a new remediation.
	w2 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if w2.Code != http.StatusCreated {
		t.Fatalf("re-enqueue %d: %s", w2.Code, w2.Body.String())
	}
	var second struct {
		Data struct {
			RemediationID string `json:"remediation_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &second)
	if second.Data.RemediationID == first.Data.RemediationID {
		t.Fatal("expected a new remediation after discard")
	}
}

func TestListScansRootsHidesChildren(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	originID := env.createScan(t, repoID)
	child := &models.Scan{
		ID: "child-1", UserID: env.UserID, RepoID: repoID,
		OriginScanID: originID, Status: models.ScanStatusCompleted,
		Branch: "wolf-fix/" + originID,
	}
	if err := env.Store.CreateScan(context.Background(), child); err != nil {
		t.Fatal(err)
	}

	all := env.doRequest(http.MethodGet, "/api/scans", nil)
	if all.Code != http.StatusOK {
		t.Fatalf("list %d", all.Code)
	}
	roots := env.doRequest(http.MethodGet, "/api/scans?roots=1", nil)
	var allResp, rootResp struct {
		Data []struct {
			ID           string `json:"id"`
			OriginScanID string `json:"origin_scan_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(all.Body.Bytes(), &allResp)
	_ = json.Unmarshal(roots.Body.Bytes(), &rootResp)
	if len(allResp.Data) < 2 {
		t.Fatalf("expected both scans in full list, got %d", len(allResp.Data))
	}
	for _, s := range rootResp.Data {
		if s.ID == "child-1" {
			t.Fatal("child should be hidden from roots list")
		}
	}
}

func TestScanLineageListsChildren(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	originID := env.createScan(t, repoID)
	child := &models.Scan{
		ID: "lin-child", UserID: env.UserID, RepoID: repoID,
		OriginScanID: originID, PreviousScanID: originID,
		Status: models.ScanStatusCompleted, Branch: "wolf-fix/" + originID,
		PreparedWorkspace: "/workspaces/x",
	}
	if err := env.Store.CreateScan(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	w := env.doRequest(http.MethodGet, "/api/scans/"+originID+"/lineage", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("lineage %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Origin   struct{ ID string } `json:"origin"`
			Children []struct {
				ID                string `json:"id"`
				OriginScanID      string `json:"origin_scan_id"`
				PreviousScanID    string `json:"previous_scan_id"`
				PreparedWorkspace string `json:"prepared_workspace"`
				Branch            string `json:"branch"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Origin.ID != originID {
		t.Fatalf("origin=%q", resp.Data.Origin.ID)
	}
	if len(resp.Data.Children) != 1 || resp.Data.Children[0].ID != "lin-child" {
		t.Fatalf("children=%+v", resp.Data.Children)
	}
	if resp.Data.Children[0].PreparedWorkspace == "" {
		t.Fatal("child should carry prepared workspace")
	}
}

func TestScanLineageIncludesLegacyFixJobs(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	originID := env.createScan(t, repoID)
	job := &models.FixJob{
		ID: "legacy-job", UserID: env.UserID, RepoID: repoID, ScanID: originID,
		Type: "fix", Status: models.FixJobSucceeded,
		TargetBranch: "wolf-fix/legacy-job", ResultBranch: "wolf-fix/legacy-job",
	}
	if err := env.Store.EnqueueFixJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	w := env.doRequest(http.MethodGet, "/api/scans/"+originID+"/lineage", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("lineage %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Agents []struct {
				ID     string `json:"id"`
				ScanID string `json:"scan_id"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Agents) != 1 || resp.Data.Agents[0].ID != "legacy-job" {
		t.Fatalf("agents=%+v", resp.Data.Agents)
	}
}

func TestLineageChildCompareByFingerprint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	originID := env.createScan(t, repoID)
	child := &models.Scan{
		ID: "cmp-child", UserID: env.UserID, RepoID: repoID,
		OriginScanID: originID, PreviousScanID: originID,
		SourceType: models.SourceTypeLocal,
		Status:     models.ScanStatusCompleted,
		Branch:     "wolf-fix/" + originID,
	}
	if err := env.Store.CreateScan(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	mustFinding := func(scanID, fp, title string) {
		t.Helper()
		if err := env.Store.CreateFinding(context.Background(), &models.Finding{
			ID: title, ScanID: scanID, RepoID: repoID,
			Fingerprint: fp, StableFingerprint: fp,
			ToolName: "bearer", Title: title, FilePath: "a.go", LineStart: 1,
			Status: models.StatusOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustFinding(originID, "stay", "stay")
	mustFinding(originID, "gone", "gone")
	mustFinding("cmp-child", "stay", "stay-after")
	mustFinding("cmp-child", "new", "new")

	w := env.doRequest(http.MethodGet, "/api/scans/"+originID+"/compare/cmp-child", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("compare %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Summary struct {
				NewCount       int `json:"new_count"`
				FixedCount     int `json:"fixed_count"`
				UnchangedCount int `json:"unchanged_count"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Summary.FixedCount != 1 || resp.Data.Summary.NewCount != 1 {
		t.Fatalf("summary=%+v want 1 fixed 1 new", resp.Data.Summary)
	}
}

func TestAcceptRemediationFreezes(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	repoID := env.createRepo(t)
	scanID := env.createScan(t, repoID)
	w1 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	var first struct {
		Data struct {
			ID            string `json:"id"`
			RemediationID string `json:"remediation_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &first)
	job, _ := env.Store.GetFixJobByID(context.Background(), first.Data.ID)
	job.Status = models.FixJobSucceeded
	_ = env.Store.UpdateFixJob(context.Background(), job)

	wAcc := env.doRequest(http.MethodPost, "/api/remediations/"+first.Data.RemediationID+"/accept", nil)
	if wAcc.Code != http.StatusOK {
		t.Fatalf("accept %d: %s", wAcc.Code, wAcc.Body.String())
	}
	w2 := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{
		"repo_id": repoID, "scan_id": scanID,
	})
	if w2.Code != http.StatusConflict {
		t.Fatalf("fix after accept %d: %s", w2.Code, w2.Body.String())
	}
}


