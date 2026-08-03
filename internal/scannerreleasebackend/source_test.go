package scannerreleasebackend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

type failFirstFetchRuntime struct {
	delegate CommandRuntime
	failed   bool
}

type captureGitRuntime struct {
	commit   string
	commands []Command
}

func (r *captureGitRuntime) Run(_ context.Context, command Command) (CommandOutput, error) {
	r.commands = append(r.commands, command)
	for _, argument := range command.Args {
		switch argument {
		case "rev-parse":
			return CommandOutput{Stdout: []byte(r.commit + "\n")}, nil
		case "fetch":
			return CommandOutput{}, errors.New("stop after capturing fetch")
		}
	}
	return CommandOutput{}, nil
}

func (r *failFirstFetchRuntime) Run(ctx context.Context, command Command) (CommandOutput, error) {
	for _, argument := range command.Args {
		if argument == "fetch" && !r.failed {
			r.failed = true
			return CommandOutput{}, errors.New("injected transient fetch failure")
		}
	}
	return r.delegate.Run(ctx, command)
}

func TestGitSourceMaterializerRestoresExactSourceForPostCheckoutBuild(t *testing.T) {
	t.Parallel()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	lockBytes, err := os.ReadFile(filepath.Join("..", "..", "scanners", "scanner-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	lockDirectory := filepath.Join(repository, "scanners")
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDirectory, "scanner-lock.yaml"), lockBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command(git, append([]string{"-C", repository}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Wolf Test", "GIT_AUTHOR_EMAIL=wolf@example.invalid",
			"GIT_COMMITTER_NAME=Wolf Test", "GIT_COMMITTER_EMAIL=wolf@example.invalid",
		)
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git %v: %v: %s", args, runErr, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init", "--quiet")
	runGit("add", "Dockerfile", "scanners/scanner-lock.yaml")
	runGit("commit", "--quiet", "-m", "fixture")
	commit := runGit("rev-parse", "HEAD")
	lock, err := scannerlock.Parse(lockBytes)
	if err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(t.TempDir(), "fresh-build-workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".wolf-release-evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, ".wolf-release-evidence", "rehydrated-marker")
	if err := os.WriteFile(marker, []byte("durable evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &failFirstFetchRuntime{delegate: ExecRuntime{MaxOutputBytes: 64 << 10}}
	materializer := &GitSourceMaterializer{
		Runtime: runtime, GitPath: git,
	}
	request := scannerreleaseworker.StepRequest{
		BuildRunID: "build-restart", CandidateID: "candidate-restart",
		BuildAttempt: 2, StepAttempt: 1, Workspace: workspace,
		DefinitionCommit: commit, LockDigest: lock.LockDigest,
		PolicyID: "policy-1", PolicyRevision: 1,
		Step: scannerpipeline.Step{
			Key: "build/default/linux-amd64", Kind: scannerpipeline.StepBuild,
		},
	}
	request.LogicalOperationID = scannerreleaseworker.DeriveLogicalOperationID(request)
	execution := scannerreleaseworkspace.ExecutionContext{SourceURL: repository}
	if err := materializer.Materialize(context.Background(), execution, request); err == nil ||
		!strings.Contains(err.Error(), "transient fetch failure") {
		t.Fatalf("injected partial materialization error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".git")); !os.IsNotExist(err) {
		t.Fatalf("partial materialization promoted a Git tree: %v", err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("source restore removed rehydrated evidence: %v", err)
	}
	restored, err := scannerlock.LoadFile(filepath.Join(workspace, scannerlock.DefaultLockPath))
	if err != nil || restored.LockDigest != request.LockDigest {
		t.Fatalf("restored lock = %#v, err=%v", restored, err)
	}
	selection, err := resolveBuildSelection(restored, "default")
	if err != nil || selection.Variant.Dockerfile == "" {
		t.Fatalf("source-dependent Buildx selection = %#v, err=%v", selection, err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err != nil {
		t.Fatalf("idempotent restored-source verification: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".wolf-release-buildx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, ".wolf-release-buildx", "operation.json"),
		[]byte(`{"digest":"sha256:test"}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err != nil {
		t.Fatalf("trusted Buildx control output was mistaken for source drift: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte("FROM poisoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err != nil {
		t.Fatalf("tracked mutation was not repaired from the exact commit: %v", err)
	}
	if value, err := os.ReadFile(filepath.Join(workspace, "Dockerfile")); err != nil ||
		string(value) != "FROM scratch\n" {
		t.Fatalf("repaired tracked source = %q, %v", value, err)
	}
	poison := filepath.Join(workspace, "poison-untracked")
	if err := os.WriteFile(poison, []byte("untracked build input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err == nil ||
		!strings.Contains(err.Error(), "differs from the exact definition commit") {
		t.Fatalf("untracked source poison error = %v", err)
	}
	if err := os.Remove(poison); err != nil {
		t.Fatal(err)
	}
	if err := materializer.Materialize(context.Background(), execution, request); err != nil {
		t.Fatalf("source did not recover after untracked poison removal: %v", err)
	}
}

func TestGitSourceMaterializerPassesAuthorizationOnlyInHTTPSProcessEnvironment(t *testing.T) {
	t.Parallel()
	commit := strings.Repeat("a", 40)
	runtime := &captureGitRuntime{commit: commit}
	const authorization = "Authorization: Bearer must-not-persist"
	materializer := &GitSourceMaterializer{
		Runtime: runtime, GitPath: "/usr/bin/git",
		Authorization: GitAuthorizationProviderFunc(func(context.Context, string) (string, error) {
			return authorization, nil
		}),
	}
	request := scannerreleaseworker.StepRequest{
		Workspace: t.TempDir(), DefinitionCommit: commit,
		LockDigest: "sha256:" + strings.Repeat("b", 64),
	}
	err := materializer.Materialize(context.Background(), scannerreleaseworkspace.ExecutionContext{
		SourceURL: "https://git.example/acme/wolf.git",
	}, request)
	if err == nil || !strings.Contains(err.Error(), "stop after capturing fetch") {
		t.Fatalf("capture materialization error = %v", err)
	}
	foundEnvironment := false
	for _, command := range runtime.commands {
		for _, argument := range command.Args {
			if strings.Contains(argument, "must-not-persist") {
				t.Fatal("Git authorization leaked into argv")
			}
		}
		for _, value := range command.Environment {
			if value == "GIT_CONFIG_VALUE_2="+authorization {
				foundEnvironment = true
			}
		}
	}
	if !foundEnvironment {
		t.Fatal("HTTPS Git authorization was not passed through the process environment")
	}

	runtime = &captureGitRuntime{commit: commit}
	materializer.Runtime = runtime
	request.Workspace = t.TempDir()
	if err := materializer.Materialize(context.Background(), scannerreleaseworkspace.ExecutionContext{
		SourceURL: "http://git.example/acme/wolf.git",
	}, request); err == nil || !strings.Contains(err.Error(), "only for credential-free HTTPS") {
		t.Fatalf("HTTP authorization error = %v", err)
	}
}
