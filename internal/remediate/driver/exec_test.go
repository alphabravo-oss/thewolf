package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecBuildsArgsWithoutCredentials(t *testing.T) {
	d := &execDriver{cfg: ExecConfig{Image: "wolf-fixer-opencode:test", Provider: "grok"}}
	args, env, containerName, err := d.buildInvocation(ExecuteRequest{
		WorktreePath: "/tmp/wt",
		AuthContent:  `{"grok":{"type":"api","key":"SECRET"}}`,
		Provider:     "grok",
		Model:        "grok-code-fast",
	}, "/tmp/cfg/opencode.json", "do the thing")
	if err != nil {
		t.Fatalf("buildInvocation: %v", err)
	}
	if containerName == "" {
		t.Error("containerName is empty; killContainer needs one to target on exhaustion")
	}

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "SECRET") {
		t.Fatalf("credential leaked into argv: %s", joined)
	}
	if !strings.Contains(joined, "--format json") && !strings.Contains(joined, "--format") {
		t.Errorf("missing --format json: %s", joined)
	}
	if !strings.Contains(joined, "--auto") {
		t.Errorf("execute run missing --auto: %s", joined)
	}
	if !strings.Contains(joined, "--name "+containerName) {
		t.Errorf("missing --name %s in argv: %s", containerName, joined)
	}

	var found bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENCODE_AUTH_CONTENT=") {
			found = true
			if !strings.Contains(kv, "SECRET") {
				t.Error("OPENCODE_AUTH_CONTENT does not carry the credential")
			}
		}
	}
	if !found {
		t.Error("OPENCODE_AUTH_CONTENT not set in env")
	}
}

func TestBuildInvocationMountsCommonGitDirForWorktreeShape(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	mainGitDir := filepath.Join(dir, "main-repo", ".git")
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt")
	if err := os.MkdirAll(worktreeGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitFile := filepath.Join(wt, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+worktreeGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &execDriver{cfg: ExecConfig{Image: "img"}}
	args, _, _, err := d.buildInvocation(ExecuteRequest{WorktreePath: wt}, "/tmp/cfg/opencode.json", "prompt")
	if err != nil {
		t.Fatalf("buildInvocation: %v", err)
	}
	want := mainGitDir + ":" + mainGitDir
	if !strings.Contains(strings.Join(args, " "), want) {
		t.Errorf("missing common git dir mount %q in argv: %v", want, args)
	}
}

func TestBuildInvocationSkipsExtraMountForPlainClone(t *testing.T) {
	dir := t.TempDir()
	// A plain clone's .git is a directory, not a worktree-pointer file.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &execDriver{cfg: ExecConfig{Image: "img"}}
	args, _, _, err := d.buildInvocation(ExecuteRequest{WorktreePath: dir}, "/tmp/cfg/opencode.json", "prompt")
	if err != nil {
		t.Fatalf("buildInvocation: %v", err)
	}
	for _, a := range args {
		if strings.Contains(a, ".git:") {
			t.Errorf("unexpected extra git-dir mount for a plain clone: %v", args)
		}
	}
}

func TestBuildInvocationRejectsMalformedWorktreePointer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &execDriver{cfg: ExecConfig{Image: "img"}}
	if _, _, _, err := d.buildInvocation(ExecuteRequest{WorktreePath: dir}, "/tmp/cfg/opencode.json", "prompt"); err == nil {
		t.Error("buildInvocation accepted a .git file that isn't a gitdir pointer, want an error")
	}
}
