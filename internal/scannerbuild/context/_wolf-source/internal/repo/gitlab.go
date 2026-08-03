package repo

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneGitLab clones a GitLab repository to the local cache.
func CloneGitLab(group, repo, branch, token string) (string, error) {
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}

	dest := filepath.Join(cacheDir, "gitlab", group, repo)
	url := fmt.Sprintf("https://gitlab.com/%s/%s.git", group, repo)

	// If already cloned, pull instead
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return dest, pullGitLabRepo(dest, token)
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
			Username: "oauth2",
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

func pullGitLabRepo(dir, token string) error {
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
			Username: "oauth2",
			Password: token,
		}
	}

	err = w.Pull(opts)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("pull: %w", err)
	}

	return nil
}
