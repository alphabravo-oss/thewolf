package repo

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneGitHub clones a GitHub repository into the local cache, or refreshes
// an existing cache and checks out the requested branch from origin.
func CloneGitHub(owner, repo, branch, token string) (string, error) {
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(cacheDir, "github", owner, repo)
	url := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	return cloneOrRefresh(dest, url, branch, githubAuth(token))
}

func githubAuth(token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	return &http.BasicAuth{Username: "x-access-token", Password: token}
}

// cloneOrRefresh clones url into dest or fetches origin and force-checkouts
// branch so a later scan cannot keep serving a stale single-branch cache.
func cloneOrRefresh(dest, url, branch string, auth transport.AuthMethod) (string, error) {
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		if err := refreshCheckout(dest, branch, auth); err != nil {
			return "", err
		}
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	opts := &git.CloneOptions{
		URL:      url,
		Auth:     auth,
		Progress: os.Stdout,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}
	if _, err := git.PlainClone(dest, false, opts); err != nil {
		return "", fmt.Errorf("clone: %w", err)
	}
	if err := refreshCheckout(dest, branch, auth); err != nil {
		return dest, err
	}
	return dest, nil
}

func refreshCheckout(dir, branch string, auth transport.AuthMethod) error {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	if err := ensureFetchAllHeads(r); err != nil {
		return err
	}
	if err := r.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Auth:       auth,
		RefSpecs:   []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		Force:      true,
		Progress:   os.Stdout,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("fetch: %w", err)
	}
	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}
	if branch == "" {
		err = w.Pull(&git.PullOptions{RemoteName: "origin", Auth: auth, Progress: os.Stdout})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("pull: %w", err)
		}
		return nil
	}
	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return fmt.Errorf("unknown branch %q: %w", branch, err)
	}
	local := plumbing.NewBranchReferenceName(branch)
	_, localErr := r.Reference(local, true)
	// go-git rejects Branch+Hash together unless Create is set.
	co := &git.CheckoutOptions{Branch: local, Force: true}
	if localErr != nil {
		co.Create = true
		co.Hash = remoteRef.Hash()
	}
	if err := w.Checkout(co); err != nil {
		return fmt.Errorf("checkout %s: %w", branch, err)
	}
	if err := w.Reset(&git.ResetOptions{Commit: remoteRef.Hash(), Mode: git.HardReset}); err != nil {
		return fmt.Errorf("reset %s: %w", branch, err)
	}
	return nil
}

func ensureFetchAllHeads(r *git.Repository) error {
	cfg, err := r.Config()
	if err != nil {
		return fmt.Errorf("read git config: %w", err)
	}
	rem, ok := cfg.Remotes["origin"]
	if !ok || rem == nil {
		return nil
	}
	want := config.RefSpec("+refs/heads/*:refs/remotes/origin/*")
	for _, s := range rem.Fetch {
		if s == want {
			return nil
		}
	}
	rem.Fetch = []config.RefSpec{want}
	cfg.Remotes["origin"] = rem
	if err := r.SetConfig(cfg); err != nil {
		return fmt.Errorf("update origin fetch spec: %w", err)
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
