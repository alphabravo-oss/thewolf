package routes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveWorkspaceDiffIncludesAllEditsOnce(t *testing.T) {
	for _, branch := range []string{"main", "topic-without-main"} {
		t.Run(branch, func(t *testing.T) {
			repo := t.TempDir()
			gitRunTest(t, repo, "init", "-b", branch)
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"committed.txt", "staged.txt", "unstaged.txt"} {
				write(name, "before\n")
			}
			gitRunTest(t, repo, "add", ".")
			gitRunTest(t, repo, "commit", "-m", "initial")
			if branch == "main" {
				gitRunTest(t, repo, "checkout", "-b", "fix")
				write("committed.txt", "committed change\n")
				gitRunTest(t, repo, "add", ".")
				gitRunTest(t, repo, "commit", "-m", "fix")
			}
			write("staged.txt", "staged change\n")
			gitRunTest(t, repo, "add", "staged.txt")
			write("unstaged.txt", "unstaged change\n")
			gitRunTest(t, repo, "config", "core.fsmonitor", "sh -c 'echo invoked > helper-executed'")
			for _, helper := range []string{"external", "textconv"} {
				gitRunTest(t, repo, "config", "diff.external", "touch helper-executed")
				if helper == "textconv" {
					gitRunTest(t, repo, "config", "--unset", "diff.external")
					write(".gitattributes", "*.txt diff=fixture\n")
					gitRunTest(t, repo, "config", "diff.fixture.textconv", "touch helper-executed")
				}
				diff := liveWorkspaceDiff(repo)
				for _, name := range []string{"staged.txt", "unstaged.txt"} {
					if strings.Count(diff, "diff --git a/"+name+" b/"+name) != 1 {
						t.Fatalf("missing/duplicated %s: %s", name, diff)
					}
				}
				if branch == "main" && !strings.Contains(diff, "+committed change") {
					t.Fatalf("lost committed edit: %s", diff)
				}
				if _, err := os.Stat(filepath.Join(repo, "helper-executed")); !os.IsNotExist(err) {
					t.Fatalf("%s helper executed: %v", helper, err)
				}
			}
		})
	}
}

func TestFilterUnifiedDiff_OneFile(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+y\n"
	got := filterUnifiedDiff(diff, []string{"a.go"})
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "+new") || strings.Contains(got, "b.go") {
		t.Fatalf("got %q", got)
	}
}
