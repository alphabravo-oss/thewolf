package routes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/google/uuid"
)

func TestExecuteQueuedScanDoesNotReplaceFailedRetainedSource(t *testing.T) {
	for _, kind := range []string{"broken-git", "missing", "file"} {
		t.Run(kind, func(t *testing.T) {
			store := withScannerSupplyChainStore(t)
			ctx := t.Context()
			user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "hash"}
			if err := store.CreateUser(ctx, user); err != nil {
				t.Fatal(err)
			}
			// A fallback would fail at a distinct path, exposing replacement without launching a scanner.
			repo := &models.Repo{ID: uuid.NewString(), UserID: user.ID, Name: "repo", SourceType: models.SourceTypeLocal, SourcePath: filepath.Join(t.TempDir(), "replacement-must-not-be-used"), DefaultBranch: "main"}
			if err := store.CreateRepo(ctx, repo); err != nil {
				t.Fatal(err)
			}
			retained := t.TempDir()
			want := "inspect existing workspace"
			switch kind {
			case "broken-git":
				if err := os.WriteFile(filepath.Join(retained, ".git"), []byte("invalid Git metadata"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				retained = filepath.Join(retained, "retained-missing")
				want = "retained-missing"
			case "file":
				retained = filepath.Join(retained, "retained-file")
				if err := os.WriteFile(retained, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "not a directory"
			}
			scan := &models.Scan{ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main", Status: models.ScanStatusPending, SourceType: models.SourceTypeLocal, PreparedWorkspace: retained, CommitSHA: "original-source-identity", TreeDigest: "original-tree-identity"}
			if err := store.CreateScan(ctx, scan); err != nil {
				t.Fatal(err)
			}
			if err := ExecuteQueuedScan(ctx, DefaultHandler, scan); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetScanByID(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != models.ScanStatusFailed || got.FailureCode != "source_prepare_failed" || !strings.Contains(got.FailureMessage, want) || strings.Contains(got.FailureMessage, "replacement-must-not-be-used") {
				t.Fatalf("retained source failure replaced: status=%s code=%s message=%s", got.Status, got.FailureCode, got.FailureMessage)
			}
			if got.CommitSHA != scan.CommitSHA || got.TreeDigest != scan.TreeDigest || got.PreparedWorkspace != retained {
				t.Fatal("failed preparation changed source provenance")
			}
		})
	}
}

// The real executor must inspect source before deleting or reusing durable results.
func TestExecuteQueuedScanRecoveryPreservesSourceAndEvidence(t *testing.T) {
	previous := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previous })
	for _, scenario := range []string{"changed", "missing-identity", "legacy-identity", "unchanged", "ssh-archive", "record-read-error", "finding-read-error", "reset-error"} {
		t.Run(scenario, func(t *testing.T) {
			store := withScannerSupplyChainStore(t)
			ctx := t.Context()
			path := t.TempDir()
			if err := os.WriteFile(filepath.Join(path, "source.go"), []byte("package before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			prepared, err := scantarget.PrepareExisting(ctx, path, models.SourceTypeLocal)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Cleanup()
			user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "hash"}
			if err := store.CreateUser(ctx, user); err != nil {
				t.Fatal(err)
			}
			repo := &models.Repo{ID: uuid.NewString(), UserID: user.ID, Name: "recovery", SourceType: models.SourceTypeLocal, SourcePath: path, DefaultBranch: "main"}
			if err := store.CreateRepo(ctx, repo); err != nil {
				t.Fatal(err)
			}
			scan := &models.Scan{ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main", Status: models.ScanStatusPending, Attempt: 2, SourceType: models.SourceTypeLocal, SourcePath: path, TreeDigest: prepared.TreeDigest, PreparedWorkspace: path, ToolsSelected: `["resume-done","resume-pending"]`, ToolsCompleted: `["resume-done"]`, ToolsFailed: `["resume-pending"]`, ToolsErrors: `{"resume-pending":"previous failure"}`, FindingCount: 2, RequestJSON: `{"tools":["resume-done","resume-pending"]}`}
			if scenario == "missing-identity" {
				scan.TreeDigest = ""
			}
			if scenario == "legacy-identity" {
				scan.TreeDigest = "git:legacy-tree-object"
			}
			if scenario == "ssh-archive" {
				scan.SourceType = models.SourceTypeSSH
				scan.SourcePath = "/remote/original/repo"
				scan.CommitSHA = "archived-commit"
				scan.DirtyState = "clean"
			}
			if err := store.CreateScan(ctx, scan); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"resume-done", "resume-pending"} {
				status := "completed"
				if tool == "resume-pending" {
					status = "failed"
				}
				if err := store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{ScanID: scan.ID, ToolName: tool, Status: status, ParserStatus: "parsed", Attempt: 1}); err != nil {
					t.Fatal(err)
				}
				if err := store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, ToolName: tool, Fingerprint: tool, Category: models.CategorySAST, Severity: models.SeverityHigh, Title: "prior evidence", FilePath: "source.go", Status: models.StatusOpen}); err != nil {
					t.Fatal(err)
				}
			}
			registry := plugin.NewRegistry()
			registry.Register(profileTestPlugin{name: "resume-done", category: models.CategorySAST})
			registry.Register(profileTestPlugin{name: "resume-pending", category: models.CategorySAST})
			if scenario == "changed" {
				if err := os.WriteFile(filepath.Join(path, "source.go"), []byte("package changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var executionStore db.Store = store
			if strings.HasSuffix(scenario, "-error") {
				executionStore = recoveryFailureStore{Store: store, step: scenario}
			}
			if err := ExecuteQueuedScan(ctx, &Handler{Store: executionStore, Registry: registry}, scan); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetScanByID(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unchanged" || scenario == "ssh-archive" {
				if got.Status != models.ScanStatusCompleted || got.TreeDigest != scan.TreeDigest || got.SourcePath != scan.SourcePath || got.CommitSHA != scan.CommitSHA {
					t.Fatalf("unchanged source did not resume: %+v", got)
				}
				records, err := store.ListScannerRunRecords(ctx, scan.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					want := 2
					if record.ToolName == "resume-done" {
						want = 1
					}
					if record.Attempt != want || record.Status != "completed" {
						t.Fatalf("wrong resumed tool state: %+v", record)
					}
				}
				findings, err := store.ListFindingsByScan(ctx, scan.ID)
				if err != nil || len(findings) != 1 || findings[0].ToolName != "resume-done" {
					t.Fatalf("completed evidence lost: %+v, %v", findings, err)
				}
				return
			}
			wantMessage := "changed"
			if strings.HasSuffix(scenario, "-error") {
				wantMessage = "fixture DB failure"
			}
			if got.Status != models.ScanStatusFailed || got.FailureCode != "source_prepare_failed" || !strings.Contains(got.FailureMessage, wantMessage) {
				t.Fatalf("changed source accepted: %s %s %s", got.Status, got.FailureCode, got.FailureMessage)
			}
			if got.TreeDigest != scan.TreeDigest {
				t.Fatal("original digest replaced")
			}
			findings, err := store.ListFindingsByScan(ctx, scan.ID)
			if err != nil || len(findings) != 2 {
				t.Fatalf("prior evidence mutated: %d, %v", len(findings), err)
			}
			records, err := store.ListScannerRunRecords(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if record.Attempt != 1 {
					t.Fatalf("tool executed on changed source: %+v", record)
				}
			}

		})
	}
}

// Embed the real store so only the recovery operation under test fails.
type recoveryFailureStore struct {
	db.Store
	step string
}

func (s recoveryFailureStore) ListScannerRunRecords(ctx context.Context, id string) ([]models.ScannerRunRecord, error) {
	if s.step == "record-read-error" {
		return nil, errors.New("fixture DB failure")
	}
	return s.Store.ListScannerRunRecords(ctx, id)
}
func (s recoveryFailureStore) ListFindingsByScan(ctx context.Context, id string) ([]models.Finding, error) {
	if s.step == "finding-read-error" {
		return nil, errors.New("fixture DB failure")
	}
	return s.Store.ListFindingsByScan(ctx, id)
}
func (s recoveryFailureStore) DeleteFindingsByScanTool(ctx context.Context, id, tool, lease string) error {
	if s.step == "reset-error" {
		return errors.New("fixture DB failure")
	}
	return s.Store.DeleteFindingsByScanTool(ctx, id, tool, lease)
}

func (s recoveryFailureStore) ListFindingEvidenceByScan(ctx context.Context, id string) ([]models.Finding, error) {
	if s.step == "finding-read-error" {
		return nil, errors.New("fixture DB failure")
	}
	return s.Store.ListFindingEvidenceByScan(ctx, id)
}
