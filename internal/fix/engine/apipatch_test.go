package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// fakeProvider returns scripted Complete responses, one per call.
type fakeProvider struct {
	replies []string
	calls   int
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Analyze(context.Context, ai.AnalyzeRequest) (*ai.AnalyzeResponse, error) {
	return nil, nil
}
func (p *fakeProvider) Score(context.Context, ai.ScoreRequest) (*ai.ScoreResponse, error) {
	return nil, nil
}
func (p *fakeProvider) Summarize(context.Context, ai.SummarizeRequest) (string, error) {
	return "", nil
}
func (p *fakeProvider) Complete(context.Context, string) (string, error) {
	i := p.calls
	p.calls++
	if i < len(p.replies) {
		return p.replies[i], nil
	}
	return p.replies[len(p.replies)-1], nil
}

// initGitRepo makes a tmp git repo with one committed file.
func initGitRepo(t *testing.T, file, content string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, out)
		}
	}
	run("git", "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "init")
	return dir
}

func TestAPIPatchEngine_AppliesValidDiff(t *testing.T) {
	repo := initGitRepo(t, "hello.txt", "hello\n")
	diff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+goodbye\n"

	eng := NewAPIPatchEngine(&fakeProvider{replies: []string{"```diff\n" + diff + "```"}})
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
	got, _ := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if strings.TrimSpace(string(got)) != "goodbye" {
		t.Errorf("file not patched, got %q", got)
	}
}

func TestAPIPatchEngine_RetriesOnBadPatchThenSucceeds(t *testing.T) {
	repo := initGitRepo(t, "hello.txt", "hello\n")
	badDiff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-WRONG CONTEXT\n+goodbye\n"
	goodDiff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+goodbye\n"

	prov := &fakeProvider{replies: []string{badDiff, goodDiff}}
	eng := NewAPIPatchEngine(prov)
	res, err := eng.Fix(context.Background(), FixRequest{
		Finding:  models.Finding{Title: "x", FilePath: "hello.txt"},
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected eventual success, got: %s", res.Error)
	}
	if prov.calls < 2 {
		t.Errorf("expected a retry (>=2 provider calls), got %d", prov.calls)
	}
}

func TestAPIPatchEngine_FailsAfterBudgetExhausted(t *testing.T) {
	repo := initGitRepo(t, "hello.txt", "hello\n")
	badDiff := "--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-NOPE\n+x\n"

	eng := NewAPIPatchEngine(&fakeProvider{replies: []string{badDiff}})
	eng.MaxAttempts = 2
	res, err := eng.Fix(context.Background(), FixRequest{
		Finding:  models.Finding{Title: "x", FilePath: "hello.txt"},
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure after budget exhausted")
	}
	if !strings.Contains(res.Error, "2 attempts") {
		t.Errorf("error should mention attempt count, got: %s", res.Error)
	}
}

func TestExtractDiff(t *testing.T) {
	plain := "diff --git a/x b/x\n--- a/x\n+++ b/x\n"
	cases := []struct{ name, in string; wantDiff bool }{
		{"fenced", "Here is the fix:\n```diff\n" + plain + "```\nDone.", true},
		{"bare", plain, true},
		{"prose only", "I cannot produce a diff.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractDiff(c.in)
			if c.wantDiff && got == "" {
				t.Error("expected a diff to be extracted")
			}
			if !c.wantDiff && got != "" {
				t.Errorf("expected no diff, got %q", got)
			}
		})
	}
}

func TestAPIPatchEngine_AvailableRequiresProvider(t *testing.T) {
	if NewAPIPatchEngine(ai.NewNoopProvider()).Available() {
		t.Error("noop provider should make api-patch unavailable")
	}
	if !NewAPIPatchEngine(&fakeProvider{}).Available() {
		t.Error("a real provider should make api-patch available")
	}
}
