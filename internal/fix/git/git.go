package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
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
	cmd := exec.Command("git", "checkout", "-b", branchName)
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
	cmd := exec.Command("git", "branch", branchName)
	cmd.Dir = repoPath
	// Ignore error if branch already exists
	_, _ = cmd.CombinedOutput() // #nosec G104 -- intentional: response/log write errors are not actionable here

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	cmd = exec.Command("git", "worktree", "add", worktreePath, branchName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %s: %w", string(output), err)
	}

	return worktreePath, nil
}

// CleanupWorktree removes a git worktree and its directory.
func CleanupWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
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
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %s: %w", string(output), err)
	}
	return string(output), nil
}

// ChangedFiles returns the list of files changed relative to HEAD.
func ChangedFiles(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %s: %w", string(output), err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
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
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = repoPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", string(out), err)
	}

	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = repoPath
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return nil
}

// RevertChanges discards all uncommitted changes in the working tree.
func RevertChanges(repoPath string) error {
	cmd := exec.Command("git", "checkout", "--", ".")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout -- .: %s: %w", string(output), err)
	}
	// Also remove any untracked files that might have been created.
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = repoPath
	if out, cleanErr := cleanCmd.CombinedOutput(); cleanErr != nil {
		return fmt.Errorf("git clean -fd: %s: %w", string(out), cleanErr)
	}
	return nil
}

// CurrentBranch returns the name of the currently checked-out branch.
func CurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --abbrev-ref HEAD: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// ListBranches returns all local and remote branch names for a repo.
// Remote tracking branches are returned without the "origin/" prefix and
// deduplicated against local branches.
func ListBranches(repoPath string) ([]string, error) {
	// Local branches
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git branch: %s: %w", string(out), err)
	}

	seen := make(map[string]bool)
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			branches = append(branches, line)
		}
	}

	// Remote branches (strip "origin/" prefix, skip HEAD pointer)
	cmd = exec.Command("git", "branch", "-r", "--format=%(refname:short)")
	cmd.Dir = repoPath
	out, err = cmd.CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasSuffix(line, "/HEAD") {
				continue
			}
			// Strip remote name prefix (e.g. "origin/feature" → "feature")
			if idx := strings.Index(line, "/"); idx >= 0 {
				line = line[idx+1:]
			}
			if !seen[line] {
				seen[line] = true
				branches = append(branches, line)
			}
		}
	}

	return branches, nil
}

// OriginURL returns `git remote get-url origin` for a local checkout.
// Empty means the path has no origin (or is not a git repo).
func OriginURL(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ListRemoteBranches returns branch names advertised by a git remote without
// requiring a local checkout.
func ListRemoteBranches(remoteURL, token string) ([]string, error) {
	opts := &gogit.ListOptions{}
	if token != "" {
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
