package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// BranchName generates the standard fix branch name.
func BranchName(scanID string, category string) string {
	// Sanitize category for branch name
	cat := strings.ReplaceAll(strings.ToLower(category), " ", "-")
	return fmt.Sprintf("wolf-fix/%s/%s", scanID, cat)
}

// CreateBranch creates a new git branch at the given repo path.
func CreateBranch(repoPath, branchName string) error {
	cmd := Command(context.Background(), nil, "checkout", "-b", branchName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout -b %s: %s: %w", branchName, string(output), err)
	}
	return nil
}

// CreateWorktree creates an isolated git worktree for fix operations.
// Returns the worktree path.
func CreateWorktree(repoPath, branchName string) (string, error) {
	worktreePath := filepath.Join(os.TempDir(), "wolf-worktree", sanitizePath(branchName))
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0750); err != nil {
		return "", fmt.Errorf("create worktree parent dir: %w", err)
	}

	// Create the branch first
	cmd := Command(context.Background(), nil, "branch", branchName)
	cmd.Dir = repoPath
	// Ignore error if branch already exists
	_, _ = cmd.CombinedOutput() // #nosec G104 -- intentional: response/log write errors are not actionable here

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	cmd = Command(context.Background(), nil, "worktree", "add", worktreePath, branchName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %s: %w", string(output), err)
	}

	return worktreePath, nil
}

// CleanupWorktree removes a git worktree and its directory.
func CleanupWorktree(repoPath, worktreePath string) error {
	cmd := Command(context.Background(), nil, "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try manual cleanup if git worktree remove fails
		os.RemoveAll(worktreePath) // #nosec G104 -- intentional: response/log write errors are not actionable here
		return fmt.Errorf("git worktree remove: %s: %w", string(output), err)
	}
	return nil
}

// CaptureDiff returns the git diff for staged and unstaged changes.
func CaptureDiff(repoPath string) (string, error) {
	// Get both staged and unstaged diff
	cmd := Command(context.Background(), nil, "diff", "--no-ext-diff", "--no-textconv", "HEAD", "--")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %s: %w", string(output), err)
	}
	return string(output), nil
}

// ChangedFiles returns the list of files changed relative to HEAD.
func ChangedFiles(repoPath string) ([]string, error) {
	cmd := Command(context.Background(), nil, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", "HEAD", "--")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %s: %w", string(output), err)
	}
	lines := strings.Split(string(output), "\x00")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// CommitAll stages and commits all changes.
func CommitAll(repoPath, message string) error {
	addCmd := Command(context.Background(), nil, "add", "-A")
	addCmd.Dir = repoPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", string(out), err)
	}

	commitCmd := Command(context.Background(), nil, "commit", "-m", message)
	commitCmd.Dir = repoPath
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return nil
}

// RevertChanges discards all uncommitted changes in the working tree.
func RevertChanges(repoPath string) error {
	cmd := Command(context.Background(), nil, "checkout", "--", ".")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout -- .: %s: %w", string(output), err)
	}
	// Also remove any untracked files that might have been created.
	cleanCmd := Command(context.Background(), nil, "clean", "-fd")
	cleanCmd.Dir = repoPath
	if out, cleanErr := cleanCmd.CombinedOutput(); cleanErr != nil {
		return fmt.Errorf("git clean -fd: %s: %w", string(out), cleanErr)
	}
	return nil
}

// CurrentBranch returns the current branch, or HEAD for a detached checkout.
func CurrentBranch(repoPath string) (string, error) {
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	gitdir, err := openWorktreeGitDir(context.Background(), root, os.OpenRoot)
	if err != nil {
		return "", err
	}
	defer func() { _ = gitdir.Close() }()
	data, err := readGitMetadata(context.Background(), gitdir, "HEAD", 4096)
	if err != nil {
		return "", err
	}
	if err := validateRefValue(data); err != nil {
		return "", err
	}
	ref := plumbing.NewReferenceFromStrings("HEAD", strings.TrimSpace(string(data)))
	if ref.Type() == plumbing.SymbolicReference {
		return ref.Target().Short(), nil
	}
	return "HEAD", nil
}

// ListBranches returns deduplicated local and remote branch names, stripping
// the remote name prefix and excluding remote HEAD pointers.
func ListBranches(repoPath string) ([]string, error) {
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// These callers already authorize the registered source, including its
	// linked worktree. Browse requests instead supply their allowlist opener.
	branches, _, err := BranchesRoot(context.Background(), root, os.OpenRoot)
	return branches, err
}

// OriginURL returns the origin from local Git configuration. Empty means the
// source has no origin or its metadata cannot be safely read.
func OriginURL(repoPath string) string {
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	origin, _ := OriginURLRoot(context.Background(), root, os.OpenRoot)
	return origin
}

// OriginURLRoot reads bounded local configuration without Git subprocesses,
// global/environment configuration, or includes. Local insteadOf rules retain
// the existing Git parser's semantics; callers authorize the resolved URL.
func OriginURLRoot(ctx context.Context, root *os.Root, openExternal func(string) (*os.Root, error)) (string, error) {
	gitdir, err := openWorktreeGitDir(ctx, root, openExternal)
	if err != nil {
		return "", err
	}
	defer func() { _ = gitdir.Close() }()
	common, err := openCommonGitDir(ctx, gitdir, openExternal)
	if err != nil {
		return "", err
	}
	defer func() { _ = common.Close() }()
	data, err := readGitMetadata(ctx, common, "config", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	cfg, err := config.ReadConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("invalid local Git configuration")
	}
	extensions := cfg.Raw.Section("extensions")
	if extensions.HasOption("worktreeConfig") {
		switch strings.ToLower(extensions.Option("worktreeConfig")) {
		case "", "true", "yes", "on", "1":
			worktreeConfig, readErr := readGitMetadata(ctx, gitdir, "config.worktree", (1<<20)-len(data))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return "", readErr
			}
			cfg, err = config.ReadConfig(bytes.NewReader(append(append(data, '\n'), worktreeConfig...)))
			if err != nil {
				return "", errors.New("invalid local Git worktree configuration")
			}
		case "false", "no", "off", "0":
		default:
			return "", errors.New("invalid Git worktreeConfig setting")
		}
	}
	if origin := cfg.Remotes["origin"]; origin != nil && len(origin.URLs) > 0 {
		return origin.URLs[0], nil
	}
	return "", nil
}

// IsGitHubTokenRemote restricts automatic GitHub credentials to their HTTPS
// origin. Repository names, URL userinfo and lookalike hosts are not authority.
func IsGitHubTokenRemote(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.User == nil &&
		strings.EqualFold(u.Hostname(), "github.com") && (u.Port() == "" || u.Port() == "443")
}

// ListRemoteBranches returns branch names advertised by a git remote without
// requiring a local checkout.
func ListRemoteBranches(remoteURL, token string) ([]string, error) {
	opts := &gogit.ListOptions{}
	if token != "" {
		if !IsGitHubTokenRemote(remoteURL) {
			return nil, fmt.Errorf("GitHub token requires an HTTPS github.com remote")
		}
		opts.Auth = &http.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	remote := gogit.NewRemote(nil, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	refs, err := remote.List(opts)
	if err != nil {
		if err == transport.ErrAuthenticationRequired {
			return nil, fmt.Errorf("authentication required")
		}
		return nil, err
	}

	seen := make(map[string]bool)
	var branches []string
	for _, ref := range refs {
		name := ref.Name()
		if !name.IsBranch() {
			continue
		}
		branch := name.Short()
		if branch != "" && !seen[branch] {
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

func sanitizePath(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}
