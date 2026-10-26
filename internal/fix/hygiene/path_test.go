package hygiene

import (
	"github.com/alphabravocompany/thewolf/internal/models"
	"os"
	"path/filepath"
	"testing"
)

func TestMechanicalFixesCannotWriteOutsideWorkspace(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.Mkdir(repo, 0750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.go")
	original := []byte("package old   \n")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "linked.go")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, "../outside.go", "linked.go"} {
		if err := replaceInRepoFile(repo, path, "old", "new"); err == nil {
			t.Fatalf("replacement accepted %s", path)
		}
		if _, err := addIgnoreComment(repo, path, models.Finding{ToolName: "gosec", RuleID: "G101", LineStart: 1}); err == nil {
			t.Fatalf("ignore accepted %s", path)
		}
	}
	trimTrailingIn(repo, []string{".go"})
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != string(original) {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "safe.go"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceInRepoFile(repo, "safe.go", "old", "new"); err != nil {
		t.Fatal(err)
	}
}
