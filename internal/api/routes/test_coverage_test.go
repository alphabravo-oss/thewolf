package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestMeasuredCoverageWorkflow(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	repoID, scanID, commit := env.createRepo(t), uuid.NewString(), strings.Repeat("a", 40)
	scan := &models.Scan{ID: scanID, UserID: env.UserID, RepoID: repoID, Branch: "main", CommitSHA: commit, DirtyState: "clean", Status: models.ScanStatusCompleted, ToolsSelected: `["trivy"]`, ToolsCompleted: `["trivy"]`, ToolsFailed: `[]`, CoverageSummary: `{"coverage_percent":100}`}
	if err := env.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/scans/" + scanID + "/test-coverage"
	w := env.doRequest("GET", endpoint, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"not_measured"`) {
		t.Fatalf("static estimate was used as measurement: %s", w.Body)
	}
	policy := &models.QualityPolicy{ID: uuid.NewString(), Name: "coverage", Scope: "repo", ScopeID: repoID, Enabled: true, RulesJSON: `[{"id":"minimum","action":"fail","test_coverage":{"metric":"lines","minimum":80}}]`}
	if err := env.Store.UpsertQualityPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	gateStatus := func(want string) {
		t.Helper()
		w := env.doRequest("GET", "/api/scans/"+scanID+"/gate", nil)
		var r struct {
			Data struct{ Evaluation struct{ Status string } }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 || r.Data.Evaluation.Status != want {
			t.Fatalf("gate want %s: %d %s", want, w.Code, w.Body)
		}
	}
	gateStatus("incomplete")
	body := map[string]any{"commit_sha": commit, "reports": []map[string]string{{"format": "lcov", "content": "SF:a.js\nDA:1,1\nDA:2,0\nBRF:0\nend_of_record\nSF:b.js\nDA:1,1\nBRF:0\nend_of_record\n"}}}
	w = env.doRequest("PUT", endpoint, body)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	gateStatus("fail")
	for _, query := range []struct{ q, path string }{{"?per_page=1&sort=coverage&order=asc", "a.js"}, {"?per_page=1&sort=coverage&order=desc", "b.js"}, {"?per_page=1&page=2&sort=path", "b.js"}, {"?q=B.JS", "b.js"}} {
		w = env.doRequest("GET", endpoint+"/files"+query.q, nil)
		var r struct {
			Data []struct{ Path string }
			Meta struct{ Total int }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 || len(r.Data) != 1 || r.Data[0].Path != query.path {
			t.Fatalf("file pagination/sort/search: %s", w.Body)
		}
	}
	// Invalid replacements must leave the accepted evidence intact.
	before, _ := env.Store.GetTestCoverage(ctx, scanID)
	for name, request := range map[string]map[string]any{
		"wrong commit":   {"commit_sha": strings.Repeat("b", 40), "reports": body["reports"]},
		"malformed":      {"commit_sha": commit, "reports": []map[string]string{{"format": "lcov", "content": "invalid"}}},
		"unknown format": {"commit_sha": commit, "reports": []map[string]string{{"format": "junit", "content": "<testsuite/>"}}},
		"unknown field":  {"commit_sha": commit, "reports": body["reports"], "coverage_percent": 100},
	} {
		t.Run(name, func(t *testing.T) {
			w := env.doRequest("PUT", endpoint, request)
			if w.Code < 400 {
				t.Fatalf("invalid upload accepted: %s", w.Body)
			}
			after, _ := env.Store.GetTestCoverage(ctx, scanID)
			if before.ReportJSON != after.ReportJSON {
				t.Fatal("invalid upload replaced good evidence")
			}
		})
	}
	// Worker status updates cannot clobber imported reports.
	if err := env.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	after, err := env.Store.GetTestCoverage(ctx, scanID)
	if err != nil || before.ReportJSON != after.ReportJSON {
		t.Fatal("scan update lost coverage")
	}
	body["reports"] = []map[string]string{{"format": "lcov", "content": "SF:a.js\nDA:1,1\nDA:2,1\nend_of_record\n"}}
	w = env.doRequest("PUT", endpoint, body)
	if w.Code != 200 {
		t.Fatalf("replacement: %s", w.Body)
	}
	gateStatus("pass")
	scan.DirtyState = "dirty"
	if err := env.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if w := env.doRequest("PUT", endpoint, body); w.Code != http.StatusConflict {
		t.Fatalf("dirty tree accepted: %d", w.Code)
	}
	gateStatus("incomplete")
}

func TestCoveragePolicyInheritance(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	repoID, collectionID, scanID := env.createRepo(t), uuid.NewString(), uuid.NewString()
	if err := env.Store.CreateCollection(ctx, &models.Collection{ID: collectionID, UserID: env.UserID, Name: "Services"}); err != nil {
		t.Fatal(err)
	}
	if err := env.Store.CreateScan(ctx, &models.Scan{ID: scanID, UserID: env.UserID, RepoID: repoID, CollectionID: &collectionID, Status: models.ScanStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ name, id string }{{"global", ""}, {"collection", collectionID}, {"repo", repoID}} {
		policy := &models.QualityPolicy{ID: uuid.NewString(), Name: "tests", Scope: scope.name, ScopeID: scope.id, Enabled: true, RulesJSON: `[{"id":"coverage","action":"fail","test_coverage":{"metric":"lines","minimum":80}}]`}
		if err := env.Store.UpsertQualityPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		w := env.doRequest("GET", "/api/scans/"+scanID+"/test-coverage", nil)
		var r struct {
			Data struct{ Policy models.QualityPolicy }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || r.Data.Policy.ID != policy.ID {
			t.Fatalf("wrong %s inheritance: %s", scope.name, w.Body)
		}
	}
	w := env.doRequest("POST", "/api/policies", map[string]any{"name": "bad", "scope": "repo", "scope_id": repoID, "rules_json": `[{"id":"coverage","action":"fail","test_coverage":{"metric":"lines","minmum":80}}]`})
	if w.Code != 400 {
		t.Fatalf("misspelled minimum accepted: %s", w.Body)
	}
}

func TestMeasuredCoverageOwnership(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	other := uuid.NewString()
	if err := env.Store.CreateUser(ctx, &models.User{ID: other, Email: "coverage-other@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: other, RepoID: env.createRepo(t), CommitSHA: strings.Repeat("a", 40), Status: models.ScanStatusCompleted}
	if err := env.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct{ method, path string }{{"GET", ""}, {"PUT", ""}, {"GET", "/files"}} {
		w := env.doRequest(action.method, "/api/scans/"+scan.ID+"/test-coverage"+action.path, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("foreign scan %s %s: %d", action.method, action.path, w.Code)
		}
	}
}
