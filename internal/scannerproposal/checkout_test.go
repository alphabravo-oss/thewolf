package scannerproposal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type proposalTestRunner struct {
	lock       []byte
	fail       string
	mu         sync.Mutex
	invocation []string
}

func (r *proposalTestRunner) Run(
	_ context.Context,
	root string,
	name string,
	args ...string,
) error {
	command := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.invocation = append(r.invocation, command)
	r.mu.Unlock()
	if command == r.fail {
		return errors.New("injected validation failure")
	}
	switch command {
	case "go run ./cmd/scannertools docs":
		return os.WriteFile(filepath.Join(root, "scanners", "TOOLS.md"), []byte("generated docs\n"), 0o644)
	case "go run ./cmd/scannertools lock --refresh-images --accept-tag-mutation --require-resolved":
		return os.WriteFile(filepath.Join(root, scannerlock.DefaultLockPath), r.lock, 0o644)
	default:
		return nil
	}
}

func TestCheckoutGeneratorUsesExactCommitAndProducesDeterministicCompleteDiff(t *testing.T) {
	t.Parallel()
	repository, commit, proposedLock, changedTool := proposalRepositoryFixture(t)
	if err := os.WriteFile(
		filepath.Join(repository, "scanners", "TOOLS.md"),
		[]byte("dirty source worktree content\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	runner := &proposalTestRunner{lock: proposedLock}
	editor := CheckoutEditorFunc(func(
		_ context.Context,
		root string,
		request scannerproposalworker.Request,
	) (CheckoutEdit, error) {
		if request.DefinitionCommit != commit {
			t.Fatalf("editor request commit = %q", request.DefinitionCommit)
		}
		current, err := os.ReadFile(filepath.Join(root, "scanners", "TOOLS.md"))
		if err != nil {
			return CheckoutEdit{}, err
		}
		if string(current) == "dirty source worktree content\n" {
			t.Fatal("generator copied source worktree instead of exact commit")
		}
		toolsPath := filepath.Join(root, "scanners", "tools.yaml")
		tools, err := os.ReadFile(toolsPath)
		if err != nil {
			return CheckoutEdit{}, err
		}
		if err := os.WriteFile(toolsPath, append(tools, []byte("\n# selected proposal edit\n")...), 0o644); err != nil {
			return CheckoutEdit{}, err
		}
		return CheckoutEdit{
			RiskSummary:   json.RawMessage(`{"reasons":["patch"],"highest_risk":"low"}`),
			RequiredGates: []string{"signature", "lock"},
			ChangeAnnotations: map[string]ChangeAnnotation{
				ChangeTool + ":" + changedTool: {
					Risk: "low", EvidenceURL: "https://evidence.example/discovery/" + changedTool,
				},
			},
			Evidence: []EvidenceLink{{
				Label: "discovery snapshot", URL: "https://evidence.example/discovery",
			}},
		}, nil
	})
	generator := CheckoutGenerator{
		RepositoryPath: repository, Editor: editor, Runner: runner,
	}
	baseLock, err := scannerlock.LoadFile(filepath.Join(repository, scannerlock.DefaultLockPath))
	if err != nil {
		t.Fatal(err)
	}
	request := scannerproposalworker.Request{
		CandidateID: "candidate-exact", DefinitionCommit: commit,
		PolicyID: "policy", PolicyRevision: 3,
		Selection: json.RawMessage(`{"mode":"explicit"}`),
		Updates: []scannerproposalworker.SelectedUpdate{{
			ID: "update-1", ComponentType: ChangeTool, ComponentName: changedTool,
			CurrentValue:   baseLock.Tools[changedTool].PinnedVersion,
			AvailableValue: baseLock.Tools[changedTool].PinnedVersion + "-proposal",
			RiskClass:      "low", Evidence: json.RawMessage(`{}`),
			Compatibility: json.RawMessage(`{}`),
		}},
	}
	first, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	normalizedFirst, err := normalizeGeneratedDefinition(first)
	if err != nil {
		t.Fatal(err)
	}
	normalizedSecond, err := normalizeGeneratedDefinition(second)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(normalizedFirst)
	secondJSON, _ := json.Marshal(normalizedSecond)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("identical exact-base generation differed\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
	if first.BaseLockDigest == first.LockDigest ||
		!strings.Contains(first.LockURI, first.LockDigest) ||
		len(first.Changes) != 1 ||
		first.Changes[0].Name != changedTool ||
		first.Changes[0].Risk != "low" {
		t.Fatalf("generated definition = %#v", first)
	}
	paths := make([]string, len(first.Files))
	for index, file := range first.Files {
		paths[index] = file.Path
	}
	for _, expected := range []string{
		"scanners/tools.yaml",
		"internal/scannerbuild/context/tools.yaml",
		"scanners/TOOLS.md",
		"scanners/scanner-lock.yaml",
	} {
		if !contains(paths, expected) {
			t.Fatalf("generated paths %v do not contain %s", paths, expected)
		}
	}
	expectedCommands := []string{
		"go run ./cmd/scannertools docs",
		"go run ./cmd/scannertools lock --refresh-images --accept-tag-mutation --require-resolved",
		"go run ./cmd/scannertools validate",
		"go run ./cmd/scannertools docs --check",
		"go run ./cmd/scannertools lock --check --require-resolved",
	}
	runner.mu.Lock()
	invocations := append([]string(nil), runner.invocation[:len(expectedCommands)]...)
	runner.mu.Unlock()
	if strings.Join(invocations, "\n") != strings.Join(expectedCommands, "\n") {
		t.Fatalf("fixed command sequence = %v", invocations)
	}
}

func TestCheckoutGeneratorValidationFailureNeverWritesProposal(t *testing.T) {
	t.Parallel()
	repository, commit, proposedLock, _ := proposalRepositoryFixture(t)
	runner := &proposalTestRunner{
		lock: proposedLock,
		fail: "go run ./cmd/scannertools validate",
	}
	provider := &recordingProvider{}
	managed := Managed{
		Git: provider,
		Generator: CheckoutGenerator{
			RepositoryPath: repository,
			Runner:         runner,
			Editor: CheckoutEditorFunc(func(
				_ context.Context,
				root string,
				_ scannerproposalworker.Request,
			) (CheckoutEdit, error) {
				tools := filepath.Join(root, "scanners", "tools.yaml")
				value, err := os.ReadFile(tools)
				if err != nil {
					return CheckoutEdit{}, err
				}
				if err := os.WriteFile(tools, append(value, []byte("\n# invalid\n")...), 0o644); err != nil {
					return CheckoutEdit{}, err
				}
				return CheckoutEdit{
					RiskSummary:   json.RawMessage(`{"highest_risk":"low"}`),
					RequiredGates: []string{"lock"},
				}, nil
			}),
		},
	}
	_, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID: "candidate-validation", DefinitionCommit: commit,
	})
	if err == nil || !strings.Contains(err.Error(), "validate scanner manifest") {
		t.Fatalf("validation failure = %v", err)
	}
	if provider.creates != 0 {
		t.Fatalf("invalid checkout wrote %d proposals", provider.creates)
	}
}

func TestCheckoutGeneratorRejectsSymlinkedParitySourceBeforeWrite(t *testing.T) {
	t.Parallel()
	repository, commit, proposedLock, _ := proposalRepositoryFixture(t)
	provider := &recordingProvider{}
	managed := Managed{
		Git: provider,
		Generator: CheckoutGenerator{
			RepositoryPath: repository,
			Runner:         &proposalTestRunner{lock: proposedLock},
			Editor: CheckoutEditorFunc(func(
				_ context.Context,
				root string,
				_ scannerproposalworker.Request,
			) (CheckoutEdit, error) {
				path := filepath.Join(root, "scanners", "tools.yaml")
				if err := os.Remove(path); err != nil {
					return CheckoutEdit{}, err
				}
				if err := os.Symlink("/etc/passwd", path); err != nil {
					return CheckoutEdit{}, err
				}
				return CheckoutEdit{
					RiskSummary:   json.RawMessage(`{"highest_risk":"critical"}`),
					RequiredGates: []string{"lock"},
				}, nil
			}),
		},
	}
	_, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID: "candidate-symlink", DefinitionCommit: commit,
	})
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink rejection = %v", err)
	}
	if provider.creates != 0 {
		t.Fatalf("symlinked checkout wrote %d proposals", provider.creates)
	}
}

func TestCheckoutGeneratorRejectsOutOfBoundsDiffBeforeWrite(t *testing.T) {
	t.Parallel()
	repository, commit, proposedLock, _ := proposalRepositoryFixture(t)
	provider := &recordingProvider{}
	managed := Managed{
		Git: provider,
		Generator: CheckoutGenerator{
			RepositoryPath: repository,
			Runner:         &proposalTestRunner{lock: proposedLock},
			Editor: CheckoutEditorFunc(func(
				_ context.Context,
				root string,
				_ scannerproposalworker.Request,
			) (CheckoutEdit, error) {
				tools := filepath.Join(root, "scanners", "tools.yaml")
				value, err := os.ReadFile(tools)
				if err != nil {
					return CheckoutEdit{}, err
				}
				if err := os.WriteFile(tools, append(value, []byte("\n# selected\n")...), 0o644); err != nil {
					return CheckoutEdit{}, err
				}
				if err := os.WriteFile(filepath.Join(root, "unexpected.txt"), []byte("escape"), 0o644); err != nil {
					return CheckoutEdit{}, err
				}
				return CheckoutEdit{
					RiskSummary:   json.RawMessage(`{"highest_risk":"high"}`),
					RequiredGates: []string{"lock"},
				}, nil
			}),
		},
	}
	_, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID: "candidate-bounds", DefinitionCommit: commit,
	})
	if err == nil || !strings.Contains(err.Error(), "out-of-bounds path") {
		t.Fatalf("path-bound rejection = %v", err)
	}
	if provider.creates != 0 {
		t.Fatalf("out-of-bounds checkout wrote %d proposals", provider.creates)
	}
}

func proposalRepositoryFixture(t *testing.T) (string, string, []byte, string) {
	t.Helper()
	sourceRoot, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	files, err := parityFiles(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if seen[file.source] {
			continue
		}
		seen[file.source] = true
		copyTestFile(t, sourceRoot, repository, file.source)
	}
	for _, relative := range []string{
		scannerlock.DefaultLockPath,
		"scanners/TOOLS.md",
	} {
		copyTestFile(t, sourceRoot, repository, relative)
	}
	if err := synchronizeParityContext(repository); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "init", "--quiet")
	runTestGit(t, repository, "add", "--all")
	runTestGit(t, repository,
		"-c", "user.name=Scanner Proposal Test",
		"-c", "user.email=scanner-proposal-test@invalid",
		"commit", "--quiet", "-m", "base definition",
	)
	commit := strings.TrimSpace(runTestGit(t, repository, "rev-parse", "HEAD"))

	base, err := scannerlock.LoadFile(filepath.Join(repository, scannerlock.DefaultLockPath))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var proposed scannerlock.Lock
	if err := json.Unmarshal(encoded, &proposed); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(proposed.Tools))
	for name, tool := range proposed.Tools {
		if tool.PinnedVersion != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("scanner lock fixture has no versioned tool")
	}
	changedTool := names[0]
	tool := proposed.Tools[changedTool]
	tool.PinnedVersion += "-proposal"
	proposed.Tools[changedTool] = tool
	proposed.LockDigest = ""
	proposed.LockDigest, err = proposed.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	proposedBytes, err := proposed.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	return repository, commit, proposedBytes, changedTool
}

func copyTestFile(t *testing.T, sourceRoot, destinationRoot, relative string) {
	t.Helper()
	source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(destinationRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, value, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func runTestGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	value, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", args[0], err, value)
	}
	return string(value)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
