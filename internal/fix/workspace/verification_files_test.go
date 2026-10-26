package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBranchChangedFilesSurviveCommitsAndReopen(t *testing.T) {
	ctx := t.Context()
	ws, err := Prepare(ctx, Options{Repo: localRepo(initRepo(t)), Branch: "wolf-fix/verification", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	base := ws.verificationBase
	if err := os.WriteFile(filepath.Join(ws.Path(), "main.go"), []byte("package main\nfunc main() { println(1) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ws.Commit(ctx, "agent commit"); err != nil {
		t.Fatal(err)
	}
	if dirty, err := ws.ChangedFiles(ctx); err != nil || len(dirty) != 0 {
		t.Fatalf("ordinary dirty-file check changed semantics: %v %v", dirty, err)
	}
	for _, name := range []string{"staged name.go", "untracked\tname.go"} {
		if err := os.WriteFile(filepath.Join(ws.Path(), name), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runGit(ctx, ws.Path(), "add", "--", "staged name.go"); err != nil {
		t.Fatal(err)
	}
	// Even moving the default branch to this HEAD must not move the saved base.
	if _, err := runGit(ctx, ws.Path(), "update-ref", "refs/heads/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ws.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	files, err := reopened.BranchChangedFiles(ctx)
	slices.Sort(files)
	if err != nil || !slices.Equal(files, []string{"main.go", "staged name.go", "untracked\tname.go"}) || reopened.verificationBase != base {
		t.Fatalf("lost branch edits across reopen: %v base=%q err=%v", files, reopened.verificationBase, err)
	}
	diff, err := reopened.Diff(ctx)
	if err != nil || !strings.Contains(diff, "+func main() { println(1) }") || !strings.Contains(diff, "staged name.go") || !strings.Contains(diff, "untracked\\tname.go") {
		t.Fatalf("review diff lost committed/staged/untracked edits: %s %v", diff, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reopened.BranchChangedFiles(cancelled); err == nil {
		t.Fatal("cancelled enumeration reported clean")
	}
	reopened.verificationBase = "--output=outside"
	if _, err := reopened.BranchChangedFiles(ctx); err == nil {
		t.Fatal("invalid metadata accepted as a Git argument")
	}
}

func TestRollbackAgentCommitSurvivesReopen(t *testing.T) {
	ctx := t.Context()
	ws, err := Prepare(ctx, Options{Repo: localRepo(initRepo(t)), Branch: "wolf-fix/rollback-commit", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	const accepted = "package main\nfunc main() { println(1) }\n"
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ws.Path(), name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", accepted)
	if err := ws.Commit(ctx, "previous accepted repair"); err != nil {
		t.Fatal(err)
	}
	if err := ws.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	base := ws.rollbackBase
	write("main.go", "rejected tracked edit\n")
	write("new file.txt", "rejected new file\n")
	write("kept.txt", "accepted independent edit\n")
	if _, err := runGit(ctx, ws.Path(), "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, ws.Path(), "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "agent commit"); err != nil {
		t.Fatal(err)
	}
	ws, err = Open(ws.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Checkpoint(ctx); err != nil || ws.rollbackBase != base {
		t.Fatalf("reopen moved the rollback checkpoint: %v", err)
	}
	write("staged.txt", "unrelated pending edit\n")
	if _, err := runGit(ctx, ws.Path(), "add", "--", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "new file.txt"} {
		if err := ws.Rollback(ctx, name); err != nil {
			t.Fatalf("rollback %s: %v", name, err)
		}
	}
	if got, err := runGit(ctx, ws.Path(), "show", "HEAD:main.go"); err != nil || got != accepted {
		t.Fatalf("rejected committed content survived: %q %v", got, err)
	}
	if files, err := runGit(ctx, ws.Path(), "ls-tree", "--name-only", "HEAD"); err != nil || strings.Contains(files, "new file.txt") || strings.Contains(files, "staged.txt") || !strings.Contains(files, "kept.txt") {
		t.Fatalf("rollback included unrelated edits or lost accepted content: %q %v", files, err)
	}
	if dirty, err := ws.ChangedFiles(ctx); err != nil || !slices.Equal(dirty, []string{"staged.txt"}) {
		t.Fatalf("unrelated staged edit lost: %v %v", dirty, err)
	}
	if err := ws.Commit(ctx, "accept surviving changes"); err != nil {
		t.Fatal(err)
	}
	ws, err = Open(ws.Path(), "")
	if err != nil || ws.rollbackBase != "" {
		t.Fatalf("accepted commit did not release checkpoint: %+v %v", ws, err)
	}
}

func TestLegacyWorkspacePinsForkPointBeforeFurtherEdits(t *testing.T) {
	ctx := t.Context()
	ws, err := Prepare(ctx, Options{Repo: localRepo(initRepo(t)), Branch: "wolf-fix/legacy", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	base := ws.verificationBase
	ws.verificationBase = ""
	if err := writeMeta(ws); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path(), "main.go"), []byte("package main\nfunc main() { println(1) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ws.Commit(ctx, "old agent commit"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ws.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	files, err := reopened.BranchChangedFiles(ctx)
	if err != nil || !slices.Equal(files, []string{"main.go"}) || reopened.verificationBase != base {
		t.Fatalf("legacy fork-point adoption: %v %v", files, err)
	}
	again, err := Open(ws.Path(), "")
	if err != nil || again.verificationBase != base {
		t.Fatalf("adopted baseline was not durable: %+v %v", again, err)
	}
}
