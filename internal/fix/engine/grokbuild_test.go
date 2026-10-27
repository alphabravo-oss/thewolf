package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Grok Build runs headless in the worktree with the key in its environment,
// and its in-place edits are reported as changed files.
func TestGrokBuildRunsHeadlessAndEditsInPlace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin, repo := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$WOLF_TEST_ARGS\"\nprintf '%s' \"$XAI_API_KEY\" > \"$WOLF_TEST_ARGS.key\"\necho fixed > app.py\n"
	if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WOLF_TEST_ARGS", argsFile)

	eng, err := NewRegistry().Resolve("grok-build")
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Fix(t.Context(), FixRequest{
		Finding:  models.Finding{Title: "SQL injection", FilePath: "app.py", LineStart: 3},
		RepoPath: repo, Timeout: 10 * time.Second, Model: "grok-4.6", Effort: "high",
		Env: []string{"XAI_API_KEY=xai-test"},
	})
	if err != nil || !res.Success || !res.EditsInPlace {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if strings.Join(res.FilesChanged, ",") != "app.py" {
		t.Fatalf("changed files = %v", res.FilesChanged)
	}
	data, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, want := range [][]string{{"--always-approve"}, {"--disable-web-search"}, {"--max-turns", "20"}, {"--cwd", repo}, {"-m", "grok-4.6"}, {"--effort", "high"}} {
		if !containsSeq(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
	if !containsSeq(args, []string{"-p"}) || !strings.Contains(strings.Join(args, " "), "SQL injection") {
		t.Fatalf("prompt not passed headless: %q", args)
	}
	if key, _ := os.ReadFile(argsFile + ".key"); string(key) != "xai-test" {
		t.Fatalf("XAI_API_KEY not passed: %q", key)
	}
}

func containsSeq(args, seq []string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// `grok models` exits 0 when signed out, so the auto chain must read its
// output before treating Grok Build as an authenticated tier.
func TestGrokAuthProbeReadsSignInState(t *testing.T) {
	for _, tc := range []struct {
		output string
		authed bool
	}{
		{"You are not authenticated.\n\nAvailable models:\n  * grok-4.6 (default)", false},
		{"Default model: grok-4.6\n\nAvailable models:\n  * grok-4.6 (default)", true},
	} {
		bin := t.TempDir()
		script := "#!/bin/sh\nprintf '%s\\n' '" + tc.output + "'\n"
		if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := defaultAuthProbe(t.Context(), "grok"); (err == nil) != tc.authed {
			t.Fatalf("output %q: authed=%v, err=%v", tc.output, tc.authed, err)
		}
	}
}
