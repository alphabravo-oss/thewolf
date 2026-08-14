package repo

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneGitHub clones a GitHub repository to the local cache.
func CloneGitHub(owner, repo, branch, token string) (string, error) {
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}

	dest := filepath.Join(cacheDir, "github", owner, repo)
	url := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)

	// If already cloned, pull instead
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return dest, pullRepo(dest, token)
	}

	opts := &git.CloneOptions{
		URL:      url,
		Progress: os.Stdout,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		opts.SingleBranch = true
	}
	if token != "" {
		opts.Auth = &http.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	_, err = git.PlainClone(dest, false, opts)
	if err != nil {
		return "", fmt.Errorf("clone: %w", err)
	}

	return dest, nil
}

func pullRepo(dir, token string) error {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	opts := &git.PullOptions{
		Progress: os.Stdout,
	}
	if token != "" {
		opts.Auth = &http.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	err = w.Pull(opts)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("pull: %w", err)
	}

	return nil
}

// CachedGitHubPath returns the on-disk scan clone for owner/repo when it
// already exists. Empty means the cache has not been populated yet.
func CachedGitHubPath(owner, repo string) string {
	owner = filepath.Clean(owner)
	repo = filepath.Clean(repo)
	if owner == "." || owner == ".." || repo == "." || repo == ".." {
		return ""
	}
	cacheDir, err := getCacheDir()
	if err != nil {
		return ""
	}
	dest := filepath.Join(cacheDir, "github", owner, repo)
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		return ""
	}
	return dest
}

func getCacheDir() (string, error) {
	if root := os.Getenv("WOLF_WORKSPACE_ROOT"); root != "" {
		return filepath.Join(root, ".wolf-cache", "repos"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".wolf", "cache", "repos"), nil
}
