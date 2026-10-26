package routes

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestScanCompletesWithoutFixingOrAIProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	previous := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previous })
	for _, aiRequested := range []bool{false, true} {
		t.Run(fmt.Sprintf("ai-requested-%t", aiRequested), func(t *testing.T) {
			store := withScannerSupplyChainStore(t)
			ctx := t.Context()
			if flag, err := store.GetSetting(ctx, "autofix_enabled"); err != nil || flag != "false" {
				t.Fatalf("autofix must default off: %q %v", flag, err)
			}
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "deps"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "deps/package-lock.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "hash"}
			if err := store.CreateUser(ctx, user); err != nil {
				t.Fatal(err)
			}
			repo := &models.Repo{ID: uuid.NewString(), UserID: user.ID, Name: "no-provider", SourceType: models.SourceTypeLocal, SourcePath: root, DefaultBranch: "main"}
			if err := store.CreateRepo(ctx, repo); err != nil {
				t.Fatal(err)
			}
			scan := &models.Scan{ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Status: models.ScanStatusPending, SourceType: models.SourceTypeLocal, SourcePath: root, PreparedWorkspace: root, Branch: "main", AIEnabled: aiRequested, RequestJSON: fmt.Sprintf(`{"tools":["cluster-a","cluster-b"],"ai_enabled":%t,"ai_engine":"anthropic"}`, aiRequested)}
			if err := store.CreateScan(ctx, scan); err != nil {
				t.Fatal(err)
			}
			h := &Handler{Store: store, Registry: completionRegistry(filepath.Join(t.TempDir(), "executed"))}
			if err := ExecuteQueuedScan(ctx, h, scan); err != nil {
				t.Fatal(err)
			}
			final, err := store.GetScanByID(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if final.Status != models.ScanStatusCompleted || final.FindingCount != 1 || final.ToolsFailed != "[]" {
				t.Fatalf("scan without provider did not complete with its finding: %+v", final)
			}
			evidence, err := store.ListFindingEvidenceByScan(ctx, scan.ID)
			if err != nil || len(evidence) != 2 {
				t.Fatalf("scanner evidence missing: %d %v", len(evidence), err)
			}
			jobs, err := store.ListFixJobsByUser(ctx, user.ID, repo.ID)
			if err != nil || len(jobs) != 0 {
				t.Fatalf("ordinary scan created fixer jobs: %+v %v", jobs, err)
			}
		})
	}
}
