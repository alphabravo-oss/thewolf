package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type actualEditsVerifier struct {
	stubVerifier
	files []string
}

func (v *actualEditsVerifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	files, err := ws.ChangedFiles(ctx)
	if err != nil {
		return nil, err
	}
	v.files = files
	return v.stubVerifier.VerifyBatch(ctx, ws, findings)
}

func TestActualEditsOverrideAgentFileClaims(t *testing.T) {
	for _, mode := range []string{"untracked", "staged", "committed", "skipped", "empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			repo := t.TempDir()
			git := func(dir string, args ...string) {
				t.Helper()
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.test")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
			}
			git(repo, "init", "-b", "main")
			git(repo, "commit", "--allow-empty", "-m", "baseline")
			store := baseStore()
			store.repo.SourcePath = repo
			finding := &models.Finding{ID: "abcdef01", ToolName: "gosec", FilePath: "source.txt", Severity: models.SeverityHigh}
			store.findings = map[string]*models.Finding{finding.ID: finding}
			ws, err := workspace.Prepare(ctx, workspace.Options{Repo: store.repo, Branch: "wolf-fix/actual", BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			editor := turnEngine(func(req engine.FixRequest) (*engine.FixResult, error) {
				if _, err := os.Stat(filepath.Join(req.RepoPath, req.FindingsFile)); err != nil {
					t.Fatal("findings review missing at invocation", err)
				}
				if mode != "empty" {
					if err := os.WriteFile(filepath.Join(req.RepoPath, "source.txt"), []byte("verified repair"), 0o600); err != nil {
						return nil, err
					}
				}
				if mode == "staged" || mode == "committed" {
					git(req.RepoPath, "add", "--", "source.txt")
				}
				if mode == "committed" {
					git(req.RepoPath, "commit", "-m", "agent repair")
				}
				result := &engine.FixResult{EditsInPlace: true, FilesChanged: []string{"invented.txt"}}
				if mode == "skipped" {
					result.Output = "SKIP: abcdef01 false positive"
				}
				return result, nil
			})
			verifier := &actualEditsVerifier{stubVerifier: stubVerifier{pass: map[string]bool{finding.ID: true}}}
			job := twoFindingJob()
			job.FindingIDList, job.WorkspacePath = []string{finding.ID}, ws.Path()
			result, err := Run(ctx, job, Deps{Store: store, Workspaces: fixedPreparer{ws}, Verifier: verifier, Diffs: &stubDiffs{},
				Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "empty" {
				if result.Summary.Kept != 0 || len(verifier.files) != 0 {
					t.Fatal("invented file changes accepted as real edits")
				}
			} else if result.Summary.Kept != 1 || !slices.Equal(verifier.files, []string{"source.txt"}) || len(store.suppressions) != 0 {
				t.Fatalf("actual edits were hidden by agent claims: %+v files=%v", result.Summary, verifier.files)
			}
		})
	}
}
