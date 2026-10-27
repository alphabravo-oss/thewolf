package routes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
)

func TestSampleRepoGitFixture(t *testing.T) {
	root := t.TempDir()
	readGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := gitpkg.Command(t.Context(), nil, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var sha string
	for _, name := range []string{"fresh", "second", "legacy"} {
		dir := filepath.Join(root, name)
		if name == "legacy" {
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(legacySampleRepoGo), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := ensureSampleRepo(t.Context(), dir); err != nil {
			t.Fatal(err)
		}
		if got := readGit(dir, "branch", "--show-current"); got != "main" {
			t.Fatalf("branch = %q", got)
		}
		if got := readGit(dir, "rev-list", "--count", "HEAD"); got != "1" {
			t.Fatalf("commit count = %q", got)
		}
		if got := readGit(dir, "status", "--porcelain"); got != "" {
			t.Fatalf("dirty sample: %s", got)
		}
		got := readGit(dir, "rev-parse", "HEAD")
		if sha != "" && sha != got {
			t.Fatalf("non-deterministic commit: %s != %s", sha, got)
		}
		sha = got
		for _, file := range []string{"main.go", "go.mod", "package.json", "package-lock.json", "secrets.env", "Dockerfile", "README.md"} {
			if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
				t.Fatal(err)
			}
		}
		const edited = "// user changes must survive\n"
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ensureSampleRepo(t.Context(), dir); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join(dir, "main.go"))
		if err != nil || string(content) != edited {
			t.Fatalf("user edit lost: %q, %v", content, err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(root, ".sample-repo-*-legacy", "main.go"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("legacy backup missing: %v, %v", backups, err)
	}
	if content, err := os.ReadFile(backups[0]); err != nil || string(content) != legacySampleRepoGo {
		t.Fatalf("legacy backup changed: %q, %v", content, err)
	}
	modified := filepath.Join(root, "modified")
	if err := os.Mkdir(modified, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modified, "main.go"), []byte("user file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSampleRepo(t.Context(), modified); err == nil {
		t.Fatal("modified non-Git directory was replaced")
	}
	if content, err := os.ReadFile(filepath.Join(modified, "main.go")); err != nil || string(content) != "user file" {
		t.Fatalf("modified non-Git file lost: %q, %v", content, err)
	}
	t.Setenv("PATH", t.TempDir())
	missingGit := filepath.Join(root, "missing-git")
	if err := ensureSampleRepo(t.Context(), missingGit); err == nil || !strings.Contains(err.Error(), "git must be installed") {
		t.Fatalf("missing git error = %v", err)
	}
	if _, err := os.Stat(missingGit); !os.IsNotExist(err) {
		t.Fatalf("partial repository published: %v", err)
	}
}
