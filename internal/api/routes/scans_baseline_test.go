package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/finding/diff"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

func TestIsZeroToolResult(t *testing.T) {
	if !isZeroToolResult(nil, nil) {
		t.Fatal("empty tools should be a zero-tool result")
	}
	if isZeroToolResult([]string{"semgrep"}, nil) {
		t.Fatal("a ran tool is not zero-tool")
	}
	if isZeroToolResult(nil, []string{"trivy"}) {
		t.Fatal("resume-completed tools are not zero-tool")
	}
}

// A scan narrowed to hand-picked tools, categories or paths is complete only
// for that slice and must not become the automatic baseline.
func TestNarrowedScansNeverBecomeAutoBaseline(t *testing.T) {
	for _, req := range []createScanRequest{{Tools: []string{"zap"}}, {Categories: []string{"sast"}}, {IncludePaths: []string{"svc/"}}} {
		if !narrowedScan(req) {
			t.Fatalf("%+v should be narrowed", req)
		}
	}
	for _, req := range []createScanRequest{{}, {Profile: "full"}, {DisabledTools: []string{"codeql"}}, {ExcludePaths: []string{"vendor/"}}} {
		if narrowedScan(req) {
			t.Fatalf("%+v is a full scan", req)
		}
	}
}

func TestRedactToolLogLine(t *testing.T) {
	in := "key=AKIATESTKEYTESTKEY12 Bearer abc.def api_key=secret-value password:hunter2"
	out := redactToolLogLine(in)
	if out == in {
		t.Fatal("expected redaction")
	}
	for _, secret := range []string{"AKIATESTKEYTESTKEY12", "abc.def", "secret-value", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q still present in %q", secret, out)
		}
	}
	if redactToolLogLine("") != "" {
		t.Fatal("empty line should stay empty")
	}
}

func TestParsePaginationCapsAt200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?per_page=50000", nil)
	_, perPage := parsePagination(req)
	if perPage != 200 {
		t.Fatalf("per_page=%d, want 200", perPage)
	}
}

func TestPersistBaselineState(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: store}
	ctx := context.Background()
	user := &models.User{ID: uuid.NewString(), Email: "base@example.test", PasswordHash: "hash"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "repo",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	t1 := time.Now().UTC().Add(-1 * time.Hour)
	t2 := time.Now().UTC()

	createScan := func(id string, completed time.Time) *models.Scan {
		t.Helper()
		scan := &models.Scan{ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`,
			ID: id, UserID: user.ID, RepoID: repo.ID, Branch: "main",
			Status: models.ScanStatusCompleted, CompletedAt: &completed,
		}
		if err := store.CreateScan(ctx, scan); err != nil {
			t.Fatalf("CreateScan %s: %v", id, err)
		}
		return scan
	}
	createFinding := func(id, scanID, fp string) {
		t.Helper()
		f := &models.Finding{
			ID: id, ScanID: scanID, RepoID: repo.ID,
			Fingerprint: fp, StableFingerprint: fp,
			ToolName: "semgrep", Category: models.CategorySAST,
			Severity: models.SeverityHigh, Title: "issue", FilePath: "a.go",
			Status: models.StatusOpen, SARIFData: "{}",
		}
		if err := store.CreateFinding(ctx, f); err != nil {
			t.Fatalf("CreateFinding %s: %v", id, err)
		}
	}
	mustFinding := func(id string) *models.Finding {
		t.Helper()
		f, err := store.GetFindingByID(ctx, id)
		if err != nil {
			t.Fatalf("GetFindingByID %s: %v", id, err)
		}
		return f
	}

	s1 := createScan("s1", t0)
	createFinding("f1", "s1", "fp-a")
	createFinding("f2", "s1", "fp-b")
	if err := persistBaselineState(ctx, h, s1); err != nil {
		t.Fatal(err)
	}
	if f := mustFinding("f1"); f.BaselineState != diff.StateNew || f.IntroducedInScanID != "s1" {
		t.Fatalf("first scan f1 = %+v", f)
	}

	s2 := createScan("s2", t1)
	createFinding("f3", "s2", "fp-a")
	createFinding("f4", "s2", "fp-c")
	if err := persistBaselineState(ctx, h, s2); err != nil {
		t.Fatal(err)
	}
	if f := mustFinding("f3"); f.BaselineState != diff.StateExisting || f.IntroducedInScanID != "s1" {
		t.Fatalf("existing f3 = %+v", f)
	}
	if f := mustFinding("f4"); f.BaselineState != diff.StateNew || f.IntroducedInScanID != "s2" {
		t.Fatalf("new f4 = %+v", f)
	}

	s3 := createScan("s3", t2)
	createFinding("f5", "s3", "fp-b")
	if err := persistBaselineState(ctx, h, s3); err != nil {
		t.Fatal(err)
	}
	if f := mustFinding("f5"); f.BaselineState != diff.StateResurfaced || f.IntroducedInScanID != "s1" {
		t.Fatalf("resurfaced f5 lost original introduction: state=%q introduced=%q", f.BaselineState, f.IntroducedInScanID)
	}
}

func TestCompleteScanCreatesFirstBaseline(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	previous := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previous })
	h := &Handler{Store: store}
	ctx := context.Background()
	user := &models.User{ID: uuid.NewString(), Email: "auto-base@example.test", PasswordHash: "hash"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "repo",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scan := &models.Scan{ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`,
		ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main",
		Status: models.ScanStatusRunning, Profile: "standard", CompletedAt: &now,
	}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}

	scan.Status, scan.Phase = models.ScanStatusCompleted, "completed"
	if _, _, err := publishScanResults(ctx, h, scan, artifactDirForScan(ctx, h, scan), report.ReportConfig{}, report.Manifest{}, db.ScanCompletion{AutoBaseline: true}); err != nil {
		t.Fatal(err)
	}
	baselines, err := store.ListScanBaselines(ctx, repo.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(baselines) != 1 || baselines[0].Name != "auto" || baselines[0].Strategy != "auto-first-success" {
		t.Fatalf("first auto baseline = %+v", baselines)
	}

	scan.ID = uuid.NewString()
	scan.Status = models.ScanStatusRunning
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	scan.Status = models.ScanStatusCompleted
	if _, _, err := publishScanResults(ctx, h, scan, artifactDirForScan(ctx, h, scan), report.ReportConfig{}, report.Manifest{}, db.ScanCompletion{AutoBaseline: true}); err != nil {
		t.Fatal(err)
	}
	baselines, err = store.ListScanBaselines(ctx, repo.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(baselines) != 1 {
		t.Fatalf("second call created duplicate baselines: %+v", baselines)
	}

	fastRepo := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "fast",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, fastRepo); err != nil {
		t.Fatal(err)
	}
	fast := &models.Scan{
		ID: uuid.NewString(), UserID: user.ID, RepoID: fastRepo.ID, Branch: "main",
		Status: models.ScanStatusRunning, Profile: "fast", CompletedAt: &now,
	}
	if err := store.CreateScan(ctx, fast); err != nil {
		t.Fatal(err)
	}
	fast.Status = models.ScanStatusCompleted
	if _, _, err := publishScanResults(ctx, h, fast, artifactDirForScan(ctx, h, fast), report.ReportConfig{}, report.Manifest{}, db.ScanCompletion{AutoBaseline: true}); err != nil {
		t.Fatal(err)
	}
	fastBaselines, err := store.ListScanBaselines(ctx, fastRepo.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(fastBaselines) != 0 {
		t.Fatalf("fast profile created baseline: %+v", fastBaselines)
	}
}

func TestPaginationRejectsArithmeticOverflow(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?page=9223372036854775807&per_page=200", nil)
	page, size := parsePagination(req)
	if (page-1)*size < 0 {
		t.Fatal("offset overflow")
	}
	start, end := paginateSlice(3, page, size)
	if start != 3 || end != 3 {
		t.Fatalf("far page: %d:%d", start, end)
	}
}
