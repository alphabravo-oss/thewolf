package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDualWriteVulnerabilitiesAndIDOR(t *testing.T) {
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

	mineRepo := e.createRepo(t)
	now := time.Now()
	mineScan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: mineRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, mineScan); err != nil {
		t.Fatalf("CreateScan mine: %v", err)
	}
	mineFinding := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: mineFinding, ScanID: mineScan.ID, RepoID: mineRepo,
		Fingerprint: "fp-mine", Title: "mine", Severity: models.SeverityHigh,
		Status: models.StatusOpen, ToolName: "semgrep", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding mine: %v", err)
	}
	routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, mineScan)

	listed := e.doRequest(http.MethodGet, "/api/vulnerabilities", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list mine: %d %s", listed.Code, listed.Body.String())
	}
	var listEnv struct {
		Data []models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listEnv); err != nil {
		t.Fatal(err)
	}
	if len(listEnv.Data) != 1 || listEnv.Data[0].Title != "mine" || listEnv.Data[0].CanonicalKey == "" {
		t.Fatalf("list = %+v", listEnv.Data)
	}
	if len(listEnv.Data[0].FindingIDs) != 1 || listEnv.Data[0].FindingIDs[0] != mineFinding {
		t.Fatalf("finding_ids = %#v", listEnv.Data[0].FindingIDs)
	}
	mineVulnID := listEnv.Data[0].ID
	if w := e.doRequest(http.MethodGet, "/api/vulnerabilities/"+mineVulnID, nil); w.Code != http.StatusOK {
		t.Fatalf("get mine: %d %s", w.Code, w.Body.String())
	}

	otherID := uuid.New().String()
	if err := e.Store.CreateUser(ctx, &models.User{
		ID: otherID, Email: "other-vuln@example.com", PasswordHash: "hash",
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
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: uuid.New().String(), ScanID: theirsScan.ID, RepoID: theirsRepo,
		Fingerprint: "fp-theirs", Title: "theirs", Severity: models.SeverityHigh,
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateFinding theirs: %v", err)
	}
	routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, theirsScan)
	theirs, err := e.Store.ListVulnerabilitiesByRepo(ctx, theirsRepo)
	if err != nil || len(theirs) != 1 {
		t.Fatalf("theirs vulns: %v %+v", err, theirs)
	}
	if w := e.doRequest(http.MethodGet, "/api/vulnerabilities/"+theirs[0].ID, nil); w.Code != http.StatusNotFound {
		t.Errorf("GET another user's vulnerability: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	listed = e.doRequest(http.MethodGet, "/api/vulnerabilities", nil)
	if err := json.Unmarshal(listed.Body.Bytes(), &listEnv); err != nil {
		t.Fatal(err)
	}
	if len(listEnv.Data) != 1 || listEnv.Data[0].ID != mineVulnID {
		t.Fatalf("list leaked other tenant: %+v", listEnv.Data)
	}
}

func TestListVulnerabilitiesBackfillsFromFindings(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	repoID := e.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CompletedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	fid := uuid.New().String()
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: fid, ScanID: scan.ID, RepoID: repoID,
		Fingerprint: "fp-backfill", Title: "backfill me",
		Severity: models.SeverityHigh, Status: models.StatusOpen,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodGet, "/api/vulnerabilities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data []models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 || env.Data[0].Title != "backfill me" {
		t.Fatalf("backfill = %+v", env.Data)
	}
	if len(env.Data[0].FindingIDs) != 1 || env.Data[0].FindingIDs[0] != fid {
		t.Fatalf("finding_ids = %#v", env.Data[0].FindingIDs)
	}
}

func TestSplitAndMergeVulnerability(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	repoID := e.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CompletedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	a := uuid.New().String()
	b := uuid.New().String()
	for _, f := range []models.Finding{
		{ID: a, ScanID: scan.ID, RepoID: repoID, Fingerprint: "fp-same", StableFingerprint: "fp-same", Title: "one",
			Severity: models.SeverityHigh, Status: models.StatusOpen, ToolName: "semgrep", CreatedAt: now, UpdatedAt: now},
		{ID: b, ScanID: scan.ID, RepoID: repoID, Fingerprint: "fp-same", StableFingerprint: "fp-same", Title: "two",
			Severity: models.SeverityHigh, Status: models.StatusOpen, ToolName: "gosec", CreatedAt: now, UpdatedAt: now},
	} {
		f := f
		if err := e.Store.CreateFinding(ctx, &f); err != nil {
			t.Fatal(err)
		}
	}
	routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, scan)
	listed := e.doRequest(http.MethodGet, "/api/vulnerabilities", nil)
	var listEnv struct {
		Data []models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listEnv); err != nil {
		t.Fatal(err)
	}
	if len(listEnv.Data) != 1 || listEnv.Data[0].EvidenceCount != 2 {
		t.Fatalf("cluster = %+v", listEnv.Data)
	}
	id := listEnv.Data[0].ID
	got := e.doRequest(http.MethodGet, "/api/vulnerabilities/"+id, nil)
	var one struct {
		Data models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one.Data.MergeReason == "" || len(one.Data.Evidence) != 2 {
		t.Fatalf("detail = %+v", one.Data)
	}

	split := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+id+"/split", map[string]any{
		"finding_ids": []string{b},
	})
	if split.Code != http.StatusOK {
		t.Fatalf("split: %d %s", split.Code, split.Body.String())
	}
	var neu struct {
		Data models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(split.Body.Bytes(), &neu); err != nil {
		t.Fatal(err)
	}
	if neu.Data.ID == id || len(neu.Data.FindingIDs) != 1 || neu.Data.FindingIDs[0] != b {
		t.Fatalf("split result = %+v", neu.Data)
	}
	orig, err := e.Store.GetVulnerabilityByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.RefreshVulnerabilityEvidence(ctx, id); err != nil {
		t.Fatal(err)
	}
	orig, err = e.Store.GetVulnerabilityByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(orig.FindingIDs) != 1 || orig.FindingIDs[0] != a {
		t.Fatalf("original after split = %+v", orig)
	}

	merged := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+id+"/merge", map[string]any{
		"vulnerability_id": neu.Data.ID,
	})
	if merged.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", merged.Code, merged.Body.String())
	}
	if _, err := e.Store.GetVulnerabilityByID(ctx, neu.Data.ID); err == nil {
		t.Fatal("source vulnerability should be deleted after merge")
	}

	otherID := uuid.New().String()
	if err := e.Store.CreateUser(ctx, &models.User{
		ID: otherID, Email: "other-split@example.com", PasswordHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	theirsRepo := uuid.New().String()
	if err := e.Store.CreateRepo(ctx, &models.Repo{
		ID: theirsRepo, UserID: otherID, Name: "theirs",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/theirs", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	theirsScan := &models.Scan{
		ID: uuid.New().String(), UserID: otherID, RepoID: theirsRepo,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, theirsScan); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: uuid.New().String(), ScanID: theirsScan.ID, RepoID: theirsRepo,
		Fingerprint: "fp-theirs-split", Title: "theirs", Severity: models.SeverityHigh,
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, theirsScan)
	theirs, err := e.Store.ListVulnerabilitiesByRepo(ctx, theirsRepo)
	if err != nil || len(theirs) != 1 {
		t.Fatalf("theirs: %v %+v", err, theirs)
	}
	if w := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+theirs[0].ID+"/split",
		map[string]any{"finding_ids": []string{"x"}}); w.Code != http.StatusNotFound {
		t.Errorf("split other tenant: expected 404, got %d %s", w.Code, w.Body.String())
	}
}
