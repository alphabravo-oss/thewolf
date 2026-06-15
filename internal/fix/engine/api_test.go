package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestAPIEngine_ReturnsDiffAndNeverEditsFiles(t *testing.T) {
	repo := initGitRepo(t, "hello.txt", "hello\n")
	diff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+goodbye\n"

	eng := NewAPIEngine(&fakeProvider{replies: []string{"```diff\n" + diff + "```"}})
	res, err := eng.Fix(context.Background(), FixRequest{
		Finding:  models.Finding{Title: "x", FilePath: "hello.txt"},
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if res.EditsInPlace {
		t.Error("APIEngine must report EditsInPlace=false")
	}
	if strings.TrimSpace(res.Diff) == "" {
		t.Error("expected a diff to be returned")
	}
	// The crux: the API engine must NOT have touched the worktree.
	got, _ := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if strings.TrimSpace(string(got)) != "hello" {
		t.Errorf("APIEngine edited the file in place; content=%q", got)
	}
	// And the worktree must be clean (no staged/unstaged changes).
	if files := changedFiles(repo); len(files) != 0 {
		t.Errorf("APIEngine dirtied the worktree: %v", files)
	}
	if len(res.FilesChanged) != 1 || res.FilesChanged[0] != "hello.txt" {
		t.Errorf("FilesChanged should reflect the diff, got %v", res.FilesChanged)
	}
}

func TestAPIEngine_NoDiffIsFailureNotEdit(t *testing.T) {
	repo := initGitRepo(t, "hello.txt", "hello\n")
	eng := NewAPIEngine(&fakeProvider{replies: []string{"I cannot produce a diff."}})
	res, err := eng.Fix(context.Background(), FixRequest{
		Finding:  models.Finding{Title: "x", FilePath: "hello.txt"},
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure when no diff is returned")
	}
	if res.EditsInPlace {
		t.Error("EditsInPlace must be false even on failure")
	}
	if files := changedFiles(repo); len(files) != 0 {
		t.Errorf("worktree must stay clean on failure, got %v", files)
	}
}

func TestAPIEngine_AvailableRequiresProvider(t *testing.T) {
	if NewAPIEngine(ai.NewNoopProvider()).Available() {
		t.Error("noop provider should make api engine unavailable")
	}
	if !NewAPIEngine(&fakeProvider{}).Available() {
		t.Error("a real provider should make api engine available")
	}
	if NewAPIEngine(nil).Available() {
		t.Error("nil provider should make api engine unavailable")
	}
}
