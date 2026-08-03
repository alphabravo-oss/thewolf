package scannerproposal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannergit"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

var fullGitCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

const maximumCommandOutput = 1 << 20

// CheckoutEditor resolves the candidate selection and applies only the
// selected definition edits inside checkoutRoot. Implementations may use the
// durable discovery store, but must never interpolate selection values into a
// shell command. CheckoutGenerator owns regeneration, validation, diff
// collection, and cleanup after Edit returns.
type CheckoutEditor interface {
	Edit(context.Context, string, scannerproposalworker.Request) (CheckoutEdit, error)
}

type CheckoutEditorFunc func(
	context.Context,
	string,
	scannerproposalworker.Request,
) (CheckoutEdit, error)

func (function CheckoutEditorFunc) Edit(
	ctx context.Context,
	root string,
	request scannerproposalworker.Request,
) (CheckoutEdit, error) {
	return function(ctx, root, request)
}

type ChangeAnnotation struct {
	Risk        string
	EvidenceURL string
}

// CheckoutEdit carries candidate/policy data that cannot be derived from the
// release lock. RequiredGates must be the immutable gate snapshot stored on
// the candidate; an empty list fails closed rather than inventing a plan from
// a newer policy revision.
type CheckoutEdit struct {
	RiskSummary        json.RawMessage
	RequiredGates      []string
	ChangeAnnotations  map[string]ChangeAnnotation
	Evidence           []EvidenceLink
	Images             []scannerpipeline.Image
	ExpectedBranchHead string
}

// CommandRunner is a test seam around the fixed-argv scannertools commands.
// ExecCommandRunner is the production implementation.
type CommandRunner interface {
	Run(context.Context, string, string, ...string) error
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(
	ctx context.Context,
	directory string,
	name string,
	args ...string,
) error {
	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- executable and argv are administrator/static configuration; candidate data is stdin/in-process only.
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf(
			"%s failed: %w: %s",
			name, err, scannerdiscovery.RedactText(strings.TrimSpace(output.String())),
		)
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if b.Len() >= maximumCommandOutput {
		return len(value), nil
	}
	remaining := maximumCommandOutput - b.Len()
	write := value
	if len(write) > remaining {
		write = write[:remaining]
	}
	_, err := b.Buffer.Write(write)
	return len(value), err
}

// CheckoutGenerator produces a failure-atomic proposal from an exact local Git
// object. The configured repository is treated as an object source only: its
// worktree, index, hooks, and uncommitted files are never used.
type CheckoutGenerator struct {
	RepositoryPath string
	RepositoryURL  string
	GitCredential  string
	TempRoot       string
	GitPath        string
	GoPath         string
	LockURIPrefix  string
	Editor         CheckoutEditor
	Runner         CommandRunner
}

var _ Generator = CheckoutGenerator{}

func (g CheckoutGenerator) Generate(
	ctx context.Context,
	request scannerproposalworker.Request,
) (GeneratedDefinition, error) {
	if g.Editor == nil {
		return GeneratedDefinition{}, errors.New("scanner proposal checkout editor is required")
	}
	if !fullGitCommitPattern.MatchString(request.DefinitionCommit) {
		return GeneratedDefinition{}, errors.New("definition commit must be a full lowercase Git SHA-1")
	}
	if (strings.TrimSpace(g.RepositoryPath) == "") == (strings.TrimSpace(g.RepositoryURL) == "") {
		return GeneratedDefinition{}, errors.New("exactly one scanner proposal repository path or URL is required")
	}
	repository := ""
	if strings.TrimSpace(g.RepositoryPath) != "" {
		var err error
		repository, err = validatedRepositoryPath(g.RepositoryPath)
		if err != nil {
			return GeneratedDefinition{}, err
		}
	} else {
		var err error
		repository, err = validatedRepositoryURL(g.RepositoryURL)
		if err != nil {
			return GeneratedDefinition{}, err
		}
		if strings.ContainsAny(g.GitCredential, "\x00\r\n") {
			return GeneratedDefinition{}, errors.New("scanner proposal Git credential is invalid")
		}
	}
	if g.GitPath == "" {
		g.GitPath = "git"
	}
	if g.GoPath == "" {
		g.GoPath = "go"
	}
	if g.LockURIPrefix == "" {
		g.LockURIPrefix = "git:scanners/scanner-lock.yaml@"
	}
	if g.Runner == nil {
		g.Runner = ExecCommandRunner{}
	}
	workspace, err := os.MkdirTemp(g.TempRoot, "wolf-scanner-definition-")
	if err != nil {
		return GeneratedDefinition{}, fmt.Errorf("create scanner proposal checkout: %w", err)
	}
	defer os.RemoveAll(workspace)

	if strings.TrimSpace(g.RepositoryPath) != "" {
		if err := cloneExactCommit(ctx, g.GitPath, repository, workspace, request.DefinitionCommit); err != nil {
			return GeneratedDefinition{}, err
		}
	} else if err := fetchExactCommit(
		ctx, g.GitPath, repository, g.GitCredential, workspace, request.DefinitionCommit,
	); err != nil {
		return GeneratedDefinition{}, err
	}
	if err := rejectCheckoutSymlinks(workspace); err != nil {
		return GeneratedDefinition{}, err
	}
	baseLock, err := scannerlock.LoadFile(filepath.Join(workspace, scannerlock.DefaultLockPath))
	if err != nil {
		return GeneratedDefinition{}, fmt.Errorf("load base scanner lock: %w", err)
	}
	edit, err := g.Editor.Edit(ctx, workspace, request)
	if err != nil {
		return GeneratedDefinition{}, fmt.Errorf("apply selected scanner updates: %w", err)
	}
	if err := rejectCheckoutSymlinks(workspace); err != nil {
		return GeneratedDefinition{}, err
	}
	if len(edit.RequiredGates) == 0 {
		return GeneratedDefinition{}, errors.New("candidate policy snapshot has no required gates")
	}
	if _, err := canonicalJSONObject(edit.RiskSummary); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("candidate risk summary: %w", err)
	}

	if err := g.Runner.Run(ctx, workspace, g.GoPath,
		"run", "./cmd/scannertools", "docs",
	); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("generate scanner documentation: %w", err)
	}
	if err := g.Runner.Run(ctx, workspace, g.GoPath,
		"run", "./cmd/scannertools", "lock",
		"--refresh-images", "--accept-tag-mutation", "--require-resolved",
	); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("generate scanner release lock: %w", err)
	}
	if err := rejectCheckoutSymlinks(workspace); err != nil {
		return GeneratedDefinition{}, err
	}
	if err := synchronizeParityContext(workspace); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("generate scanner parity context: %w", err)
	}

	validation := []Validation{
		{Name: "manifest", Status: "passed", Command: "go run ./cmd/scannertools validate"},
		{Name: "docs", Status: "passed", Command: "go run ./cmd/scannertools docs --check"},
		{Name: "parity", Status: "passed", Command: "internal byte-for-byte scanner/context parity validation"},
		{Name: "lock", Status: "passed", Command: "go run ./cmd/scannertools lock --check --require-resolved"},
	}
	if err := g.Runner.Run(ctx, workspace, g.GoPath,
		"run", "./cmd/scannertools", "validate",
	); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("validate scanner manifest: %w", err)
	}
	if err := g.Runner.Run(ctx, workspace, g.GoPath,
		"run", "./cmd/scannertools", "docs", "--check",
	); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("validate scanner documentation: %w", err)
	}
	if err := validateParityContext(workspace); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("validate scanner parity context: %w", err)
	}
	if err := g.Runner.Run(ctx, workspace, g.GoPath,
		"run", "./cmd/scannertools", "lock", "--check", "--require-resolved",
	); err != nil {
		return GeneratedDefinition{}, fmt.Errorf("validate scanner release lock: %w", err)
	}
	if err := rejectCheckoutSymlinks(workspace); err != nil {
		return GeneratedDefinition{}, err
	}

	proposedLock, err := scannerlock.LoadFile(filepath.Join(workspace, scannerlock.DefaultLockPath))
	if err != nil {
		return GeneratedDefinition{}, fmt.Errorf("load proposed scanner lock: %w", err)
	}
	files, err := collectProposalFiles(ctx, g.GitPath, workspace)
	if err != nil {
		return GeneratedDefinition{}, err
	}
	changes, err := deriveLockChanges(baseLock, proposedLock, edit.ChangeAnnotations)
	if err != nil {
		return GeneratedDefinition{}, err
	}
	if err := validateSelectedChanges(request.Updates, changes); err != nil {
		return GeneratedDefinition{}, err
	}
	gates, err := proposalGates(edit.RequiredGates)
	if err != nil {
		return GeneratedDefinition{}, err
	}
	images := append([]scannerpipeline.Image(nil), edit.Images...)
	if len(images) == 0 {
		images = lockImages(proposedLock)
	}
	lockURI := g.LockURIPrefix + proposedLock.LockDigest
	generated := GeneratedDefinition{
		Files: files, BaseLockDigest: baseLock.LockDigest,
		LockDigest: proposedLock.LockDigest, LockURI: lockURI,
		DefinitionDigest:   proposedLock.Definition.Digest,
		DiffDigest:         generatedFilesDigest(files),
		RiskSummary:        append(json.RawMessage(nil), edit.RiskSummary...),
		Changes:            changes,
		Gates:              gates,
		Evidence:           append([]EvidenceLink(nil), edit.Evidence...),
		Validation:         validation,
		Images:             images,
		ExpectedBranchHead: edit.ExpectedBranchHead,
	}
	return normalizeGeneratedDefinition(generated)
}

func validateSelectedChanges(
	updates []scannerproposalworker.SelectedUpdate,
	changes []Change,
) error {
	if len(updates) == 0 {
		return errors.New("proposal has no server-resolved selected updates")
	}
	byKey := make(map[string]Change, len(changes))
	for _, change := range changes {
		byKey[change.Kind+":"+change.Name] = change
	}
	allowed := make(map[string]bool, len(updates))
	for _, update := range updates {
		key, err := editorChangeKey(update)
		if err != nil {
			return err
		}
		allowed[key] = true
		change, exists := byKey[key]
		if !exists {
			return fmt.Errorf("selected scanner update %s produced no %s lock change", update.ID, key)
		}
		switch update.ComponentType {
		case ChangeTool:
			if change.To != update.AvailableValue {
				return fmt.Errorf(
					"selected tool %s resolved to %q, expected %q",
					update.ComponentName, change.To, update.AvailableValue,
				)
			}
		case "upstream_image", ChangeBaseImage:
			if change.Digest != update.AvailableDigest {
				return fmt.Errorf(
					"selected %s %s resolved to %q, expected %q",
					update.ComponentType, update.ComponentName,
					change.Digest, update.AvailableDigest,
				)
			}
		case ChangeToolchain:
			if update.ComponentName == "rust" {
				if change.To != update.AvailableValue {
					return fmt.Errorf("selected Rust toolchain resolved to %q, expected %q", change.To, update.AvailableValue)
				}
				continue
			}
			var values map[string]string
			if err := json.Unmarshal([]byte(change.To), &values); err != nil ||
				values["version"] != update.AvailableValue {
				return fmt.Errorf(
					"selected toolchain %s does not contain expected version %q",
					update.ComponentName, update.AvailableValue,
				)
			}
		}
	}
	for key := range byKey {
		if !allowed[key] {
			return fmt.Errorf("proposal produced unselected scanner definition change %s", key)
		}
	}
	return nil
}

func validatedRepositoryPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("scanner proposal repository path is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve scanner proposal repository: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve scanner proposal repository symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect scanner proposal repository: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("scanner proposal repository must be a directory")
	}
	return resolved, nil
}

func validatedRepositoryURL(value string) (string, error) {
	if len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("scanner proposal repository URL is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("scanner proposal repository URL must be credential-free HTTPS")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	if parsed.Path == "" || parsed.Path == "." {
		return "", errors.New("scanner proposal repository URL must include a repository path")
	}
	return parsed.String(), nil
}

func cloneExactCommit(
	ctx context.Context,
	gitPath, repository, workspace, commit string,
) error {
	if _, err := gitCommand(ctx, gitPath, "", "clone", "--quiet",
		"--no-checkout", "--no-hardlinks", "--local", "--", repository, workspace,
	); err != nil {
		return fmt.Errorf("clone scanner definition object store: %w", err)
	}
	if _, err := gitCommand(ctx, gitPath, workspace,
		"cat-file", "-e", commit+"^{commit}",
	); err != nil {
		return fmt.Errorf("resolve exact scanner definition commit: %w", err)
	}
	if _, err := gitCommand(ctx, gitPath, workspace,
		"checkout", "--quiet", "--detach", "--force", "--no-recurse-submodules", commit, "--",
	); err != nil {
		return fmt.Errorf("check out exact scanner definition commit: %w", err)
	}
	head, err := gitCommand(ctx, gitPath, workspace, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("verify scanner definition checkout: %w", err)
	}
	if strings.TrimSpace(string(head)) != commit {
		return errors.New("scanner definition checkout did not resolve to the requested commit")
	}
	return nil
}

func fetchExactCommit(
	ctx context.Context,
	gitPath, repositoryURL, credential, workspace, commit string,
) error {
	if _, err := gitCommand(ctx, gitPath, "", "init", "--quiet", "--", workspace); err != nil {
		return fmt.Errorf("initialize scanner definition checkout: %w", err)
	}
	if _, err := gitCommand(
		ctx, gitPath, workspace, "remote", "add", "origin", repositoryURL,
	); err != nil {
		return fmt.Errorf("configure scanner definition origin: %w", err)
	}
	environment := gitHTTPEnvironment(credential)
	if _, err := gitCommandEnvironment(
		ctx, gitPath, workspace, environment,
		"fetch", "--quiet", "--depth=1", "--no-tags", "origin", commit,
	); err != nil {
		return fmt.Errorf("fetch exact scanner definition commit: %w", err)
	}
	if _, err := gitCommand(
		ctx, gitPath, workspace,
		"checkout", "--quiet", "--detach", "--force", "--no-recurse-submodules", commit, "--",
	); err != nil {
		return fmt.Errorf("check out fetched scanner definition commit: %w", err)
	}
	head, err := gitCommand(ctx, gitPath, workspace, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("verify fetched scanner definition checkout: %w", err)
	}
	if strings.TrimSpace(string(head)) != commit {
		return errors.New("fetched scanner definition checkout did not resolve to the requested commit")
	}
	return nil
}

func gitHTTPEnvironment(credential string) []string {
	if credential == "" {
		return nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + credential))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
	}
}

func gitCommand(
	ctx context.Context,
	gitPath, directory string,
	args ...string,
) ([]byte, error) {
	return gitCommandEnvironment(ctx, gitPath, directory, nil, args...)
}

func gitCommandEnvironment(
	ctx context.Context,
	gitPath, directory string,
	extraEnvironment []string,
	args ...string,
) ([]byte, error) {
	fixed := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	command := exec.CommandContext(ctx, gitPath, fixed...) // #nosec G204 -- no shell; commit is strict-validated and paths are administrator/temp configuration.
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
	)
	command.Env = append(command.Env, extraEnvironment...)
	var stderr boundedBuffer
	command.Stderr = &stderr
	value, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w: %s",
			args[0], err,
			scannerdiscovery.RedactText(strings.TrimSpace(stderr.String())),
		)
	}
	return value, nil
}

func synchronizeParityContext(root string) error {
	files, err := parityFiles(root)
	if err != nil {
		return err
	}
	contextRoot := filepath.Join(root, "internal", "scannerbuild", "context")
	if err := rejectSymlink(contextRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(contextRoot); err != nil {
		return err
	}
	for _, file := range files {
		source := filepath.Join(root, filepath.FromSlash(file.source))
		destination := filepath.Join(root, filepath.FromSlash(file.destination))
		if err := copyParityFile(source, destination); err != nil {
			return err
		}
	}
	return nil
}

func rejectCheckoutSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == filepath.Join(root, ".git") && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return fmt.Errorf(
				"scanner proposal checkout path %s must not be a symlink",
				filepath.ToSlash(relative),
			)
		}
		return nil
	})
}

type parityFile struct {
	source      string
	destination string
}

func parityFiles(root string) ([]parityFile, error) {
	var files []parityFile
	addFile := func(source, destination string) error {
		full := filepath.Join(root, filepath.FromSlash(source))
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("scanner parity source %s must be a regular file", source)
		}
		files = append(files, parityFile{source: source, destination: destination})
		return nil
	}
	for _, name := range []string{
		".dockerignore", "os-packages.lock.yaml", "os-packages.yaml",
		"toolchains.yaml", "tools.yaml", "versions.env", "smoke-test.sh",
		"wolf-tool-entry", "trufflehog-excludes.txt",
	} {
		if err := addFile("scanners/"+name, "internal/scannerbuild/context/"+name); err != nil {
			return nil, err
		}
	}
	scannerEntries, err := os.ReadDir(filepath.Join(root, "scanners"))
	if err != nil {
		return nil, err
	}
	for _, entry := range scannerEntries {
		if strings.HasPrefix(entry.Name(), "Dockerfile") {
			if err := addFile(
				"scanners/"+entry.Name(),
				"internal/scannerbuild/context/"+entry.Name(),
			); err != nil {
				return nil, err
			}
		}
	}
	for _, directory := range []string{"install", "os-packages"} {
		base := filepath.Join(root, "scanners", directory)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("scanner parity source %s must not be a symlink", path)
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("scanner parity source %s must be a regular file", path)
			}
			relative, err := filepath.Rel(filepath.Join(root, "scanners"), path)
			if err != nil {
				return err
			}
			return addFile(
				filepath.ToSlash(filepath.Join("scanners", relative)),
				filepath.ToSlash(filepath.Join("internal/scannerbuild/context", relative)),
			)
		})
		if err != nil {
			return nil, err
		}
	}
	fixerEntries, err := os.ReadDir(filepath.Join(root, "fixer"))
	if err != nil {
		return nil, err
	}
	for _, entry := range fixerEntries {
		if strings.HasPrefix(entry.Name(), "Dockerfile") {
			if err := addFile(
				"fixer/"+entry.Name(),
				"internal/scannerbuild/context/fixer/"+entry.Name(),
			); err != nil {
				return nil, err
			}
		}
	}
	if err := addFile(
		"fixer/versions.env", "internal/scannerbuild/context/fixer/versions.env",
	); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].destination < files[j].destination
	})
	return files, nil
}

func copyParityFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("scanner parity source %s must be a regular file", source)
	}
	value, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destination, value, info.Mode().Perm())
}

func validateParityContext(root string) error {
	files, err := parityFiles(root)
	if err != nil {
		return err
	}
	expected := make(map[string]bool, len(files))
	for _, file := range files {
		source := filepath.Join(root, filepath.FromSlash(file.source))
		destination := filepath.Join(root, filepath.FromSlash(file.destination))
		if err := rejectSymlink(destination); err != nil {
			return err
		}
		sourceValue, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		destinationValue, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		if !bytes.Equal(sourceValue, destinationValue) {
			return fmt.Errorf("scanner parity file %s differs from %s", file.destination, file.source)
		}
		expected[filepath.Clean(destination)] = true
	}
	contextRoot := filepath.Join(root, "internal", "scannerbuild", "context")
	return filepath.WalkDir(contextRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("scanner parity output %s must not be a symlink", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !expected[filepath.Clean(path)] {
			return fmt.Errorf("scanner parity output contains unexpected file %s", path)
		}
		return nil
	})
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("scanner proposal path %s must not be a symlink", path)
	}
	return nil
}

func collectProposalFiles(
	ctx context.Context,
	gitPath, workspace string,
) ([]scannergit.File, error) {
	if _, err := gitCommand(ctx, gitPath, workspace,
		"add", "--intent-to-add", "--all", "--",
	); err != nil {
		return nil, fmt.Errorf("index generated scanner proposal paths: %w", err)
	}
	value, err := gitCommand(ctx, gitPath, workspace,
		"diff", "--name-only", "--no-renames", "--no-ext-diff",
		"--no-textconv", "-z", "HEAD", "--",
	)
	if err != nil {
		return nil, fmt.Errorf("collect generated scanner proposal paths: %w", err)
	}
	names := bytes.Split(value, []byte{0})
	files := make([]scannergit.File, 0, len(names))
	for _, encoded := range names {
		if len(encoded) == 0 {
			continue
		}
		name := string(encoded)
		if !allowedProposalPath(name) {
			return nil, fmt.Errorf("generated proposal changed out-of-bounds path %q", name)
		}
		full := filepath.Join(workspace, filepath.FromSlash(name))
		info, statErr := os.Lstat(full)
		if os.IsNotExist(statErr) {
			files = append(files, scannergit.File{
				Path: name, Mode: "100644", Delete: true,
			})
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("generated proposal path %q must be a regular file", name)
		}
		content, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		mode := "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		files = append(files, scannergit.File{
			Path: name, Content: content, Mode: mode,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	if len(files) == 0 {
		return nil, errors.New("selected scanner updates produced no definition diff")
	}
	return files, nil
}

func allowedProposalPath(value string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean != value || clean == "." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, prefix := range []string{
		"scanners/",
		"internal/scannerbuild/context/",
		"docs/superpowers/changelog/scanner-bumps/",
	} {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

func deriveLockChanges(
	base, proposed *scannerlock.Lock,
	annotations map[string]ChangeAnnotation,
) ([]Change, error) {
	var changes []Change
	usedAnnotations := make(map[string]bool, len(annotations))
	for _, name := range unionKeys(base.Tools, proposed.Tools) {
		before, beforeOK := base.Tools[name]
		after, afterOK := proposed.Tools[name]
		baseImage, baseImageOK := base.UpstreamImages[name]
		proposedImage, proposedImageOK := proposed.UpstreamImages[name]
		if equalJSON(before, after) && beforeOK == afterOK &&
			equalJSON(baseImage, proposedImage) && baseImageOK == proposedImageOK {
			continue
		}
		from := toolDisplay(before, beforeOK, baseImage, baseImageOK)
		to := toolDisplay(after, afterOK, proposedImage, proposedImageOK)
		digest := ""
		if afterOK {
			digest = normalizeSHA256(after.SourceIntegrity.SHA256)
		}
		if proposedImageOK && proposedImage.Digest != "" {
			digest = proposedImage.Digest
		}
		key := ChangeTool + ":" + name
		changes = append(changes, annotatedChange(
			ChangeTool, name, from, to, digest, annotations,
		))
		usedAnnotations[key] = true
	}
	for _, name := range unionKeys(base.BaseImages, proposed.BaseImages) {
		before, beforeOK := base.BaseImages[name]
		after, afterOK := proposed.BaseImages[name]
		if equalJSON(before, after) && beforeOK == afterOK {
			continue
		}
		key := ChangeBaseImage + ":" + name
		changes = append(changes, annotatedChange(
			ChangeBaseImage, name,
			componentDisplay(before.Reference, beforeOK),
			componentDisplay(after.Reference, afterOK),
			after.Digest, annotations,
		))
		usedAnnotations[key] = true
	}
	for _, name := range unionKeys(base.Toolchains, proposed.Toolchains) {
		before, beforeOK := base.Toolchains[name]
		after, afterOK := proposed.Toolchains[name]
		if equalJSON(before, after) && beforeOK == afterOK {
			continue
		}
		key := ChangeToolchain + ":" + name
		changes = append(changes, annotatedChange(
			ChangeToolchain, name,
			componentJSONDisplay(before.Values, beforeOK),
			componentJSONDisplay(after.Values, afterOK),
			valueDigest(after.Values, afterOK), annotations,
		))
		usedAnnotations[key] = true
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Name < changes[j].Name
	})
	if len(changes) == 0 {
		return nil, errors.New("proposed lock contains no tool, base-image, or toolchain changes")
	}
	for key := range annotations {
		if !usedAnnotations[key] {
			return nil, fmt.Errorf(
				"change annotation %q does not match a lock-derived change", key,
			)
		}
	}
	return changes, nil
}

func annotatedChange(
	kind, name, from, to, digest string,
	annotations map[string]ChangeAnnotation,
) Change {
	annotation := annotations[kind+":"+name]
	return Change{
		Kind: kind, Name: name, From: from, To: to, Digest: digest,
		Risk: annotation.Risk, EvidenceURL: annotation.EvidenceURL,
	}
}

func unionKeys[V any](left, right map[string]V) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for key := range left {
		seen[key] = true
	}
	for key := range right {
		seen[key] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalJSON(left, right any) bool {
	leftValue, _ := json.Marshal(left)
	rightValue, _ := json.Marshal(right)
	return bytes.Equal(leftValue, rightValue)
}

func toolDisplay(
	tool scannerlock.Tool,
	exists bool,
	image scannerlock.UpstreamImage,
	imageExists bool,
) string {
	if !exists {
		return "<absent>"
	}
	if tool.PinnedVersion != "" {
		return tool.PinnedVersion
	}
	if imageExists {
		if image.ResolvedReference != "" {
			return image.ResolvedReference
		}
		return image.DeclaredReference
	}
	value, _ := json.Marshal(tool)
	return shortDigest(value)
}

func componentDisplay(value string, exists bool) string {
	if !exists {
		return "<absent>"
	}
	return value
}

func componentJSONDisplay(value map[string]string, exists bool) string {
	if !exists {
		return "<absent>"
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func valueDigest(value any, exists bool) string {
	if !exists {
		return ""
	}
	encoded, _ := json.Marshal(value)
	return shortDigest(encoded)
}

func shortDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeSHA256(value string) string {
	if regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value) {
		return "sha256:" + value
	}
	if proposalDigestPattern.MatchString(value) {
		return value
	}
	return ""
}

func proposalGates(required []string) ([]GatePlan, error) {
	seen := make(map[string]bool, len(required))
	gates := make([]GatePlan, 0, len(required))
	for _, name := range required {
		if !proposalNamePattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("candidate required gate %q is invalid", name)
		}
		seen[name] = true
		gates = append(gates, GatePlan{Name: name, Status: "pending"})
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].Name < gates[j].Name })
	return gates, nil
}

func lockImages(lock *scannerlock.Lock) []scannerpipeline.Image {
	keys := make([]string, 0, len(lock.ReleaseInputs.Variants))
	for key := range lock.ReleaseInputs.Variants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	images := make([]scannerpipeline.Image, 0, len(keys))
	for _, key := range keys {
		images = append(images, scannerpipeline.Image{
			Key:       key,
			Kind:      scannerpipeline.ImageKindScanner,
			Platforms: append([]string(nil), lock.ReleaseInputs.Variants[key].Platforms...),
		})
	}
	fixerKeys := make([]string, 0, len(lock.ReleaseInputs.FixerVariants))
	for key := range lock.ReleaseInputs.FixerVariants {
		fixerKeys = append(fixerKeys, key)
	}
	sort.Strings(fixerKeys)
	for _, key := range fixerKeys {
		variant := lock.ReleaseInputs.FixerVariants[key]
		dependencies := make([]string, 0, len(variant.DependsOn))
		for _, dependency := range variant.DependsOn {
			dependencies = append(dependencies, "fixer-"+dependency)
		}
		images = append(images, scannerpipeline.Image{
			Key:       "fixer-" + key,
			Kind:      scannerpipeline.ImageKindFixer,
			Platforms: append([]string(nil), variant.Platforms...),
			DependsOn: dependencies,
		})
	}
	return images
}
