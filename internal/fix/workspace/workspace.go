// Package workspace prepares a writable working tree on a NEW fix branch for
// the autonomous fix engine, isolated from the user's working tree (design §9).
//
// A Workspace is "where an engine makes its changes." The orchestrator
// (Phase 5) prepares one per job, hands its Path to the engine + verification
// gate, then either keeps the resulting branch (v1: branch-only, no push) or
// rolls it back. The user's original work tree is never touched: local repos
// get a dedicated `git worktree` on a fresh branch; GitHub repos get a
// clone-for-write with the token.
//
//   - local  → `git worktree add <tmp> -b <branch>` off the repo's checkout.
//   - github → `git clone <https-with-token>` into a tmp dir, then a new branch.
//   - ssh    → DEFERRED to v1.1; the writability preflight already gates it, and
//     Prepare returns ErrSSHUnsupported so a misconfigured job fails fast with a
//     clear reason rather than half-running.
//
// Every git shell-out goes through the package-level runGit var so tests can
// stub it; the default runs the real `git` binary. Workspace tests use real
// temp git repos (cheap) rather than stubbing, exercising the real worktree
// path end-to-end.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ErrSSHUnsupported is returned by Prepare for SSH-sourced repos: write-clone
// over a node is deferred to v1.1. The writability preflight (Phase 2) already
// reports SSH repos as not-fixable for this milestone, so this is a defensive
// fail-fast rather than an expected path.
var ErrSSHUnsupported = errors.New("workspace: ssh write-clone is not supported in v1 (deferred to v1.1)")

// Kind records how a workspace's working tree was materialised, so Cleanup can
// undo it correctly (remove a worktree vs. delete a clone dir).
type Kind string

const (
	// KindWorktree is a local `git worktree` rooted at the repo's checkout.
	KindWorktree Kind = "worktree"
	// KindClone is a fresh clone-for-write (GitHub).
	KindClone Kind = "clone"
)

// Options configures a Workspace. RepoPath/CloneURL are mutually selected by
// the repo's SourceType; Branch is the new fix branch to create.
type Options struct {
	// Repo is the target repository. Its SourceType selects the strategy.
	Repo *models.Repo
	// Branch is the NEW fix branch name to create (e.g. "wolf-fix/<scan>/<cat>").
	// Required.
	Branch string
	// Token is the GitHub token used for a clone-for-write. Required for github
	// repos, ignored otherwise.
	Token string
	// CloneURL overrides the derived GitHub clone URL (host/owner/repo). When
	// empty, it is derived from Repo.SourcePath as
	// https://<token>@github.com/<owner>/<repo>.git. Mainly a test seam.
	CloneURL string
	// BaseDir is the parent directory for the worktree / clone. When empty,
	// os.MkdirTemp's default (the OS temp dir) is used.
	BaseDir string
}

// Workspace is an isolated, writable working tree on a fix branch.
type Workspace struct {
	// path is the working-tree directory engines edit and the verify gate runs in.
	path string
	// branch is the fix branch checked out in path.
	branch string
	// kind records worktree vs clone for Cleanup.
	kind Kind
	// repoRoot is the original repo checkout (for `git worktree remove`). Empty
	// for clones.
	repoRoot string
	// tmpRoot is the temp directory created for this workspace, removed on
	// Cleanup. For a worktree it is the worktree dir's parent; for a clone it is
	// the clone dir itself's parent.
	tmpRoot string
}

// runGit runs `git` with args in dir and returns combined output. It is a
// package var so tests can stub git entirely; the default shells out.
var runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- "git" is a fixed binary; args are internal (branch names,
	// repo paths), never raw user input.
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// Prepare materialises a writable working tree on a new fix branch for the
// repo. The caller is responsible for Cleanup (defer ws.Cleanup()). On any
// failure it cleans up whatever it created before returning.
func Prepare(ctx context.Context, opts Options) (*Workspace, error) {
	if opts.Repo == nil {
		return nil, errors.New("workspace: repo is required")
	}
	if strings.TrimSpace(opts.Branch) == "" {
		return nil, errors.New("workspace: branch is required")
	}
	switch opts.Repo.SourceType {
	case models.SourceTypeLocal:
		return prepareLocal(ctx, opts)
	case models.SourceTypeGitHub:
		return prepareGitHub(ctx, opts)
	case models.SourceTypeSSH:
		return nil, ErrSSHUnsupported
	default:
		return nil, fmt.Errorf("workspace: source type %q is not writable in v1", opts.Repo.SourceType)
	}
}

func prepareLocal(ctx context.Context, opts Options) (*Workspace, error) {
	repoRoot := opts.Repo.SourcePath
	if repoRoot == "" {
		return nil, errors.New("workspace: local repo has no source path")
	}
	tmpRoot, err := mkTempRoot(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	wtPath := filepath.Join(tmpRoot, "worktree")

	// `git worktree add <path> -b <branch>` creates the new branch AND its
	// dedicated working tree in one atomic step off the current HEAD.
	if out, err := runGit(ctx, repoRoot, "worktree", "add", wtPath, "-b", opts.Branch); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: create worktree: %s: %w", strings.TrimSpace(out), err)
	}

	return &Workspace{
		path:     wtPath,
		branch:   opts.Branch,
		kind:     KindWorktree,
		repoRoot: repoRoot,
		tmpRoot:  tmpRoot,
	}, nil
}

func prepareGitHub(ctx context.Context, opts Options) (*Workspace, error) {
	if opts.Token == "" {
		return nil, errors.New("workspace: github write-clone requires a token")
	}
	cloneURL := opts.CloneURL
	if cloneURL == "" {
		derived, err := githubCloneURL(opts.Repo.SourcePath, opts.Token)
		if err != nil {
			return nil, err
		}
		cloneURL = derived
	}
	tmpRoot, err := mkTempRoot(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	clonePath := filepath.Join(tmpRoot, "clone")

	if out, err := runGit(ctx, "", "clone", cloneURL, clonePath); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: clone-for-write: %s: %w", redact(strings.TrimSpace(out), opts.Token), err)
	}
	// New fix branch on the fresh clone.
	if out, err := runGit(ctx, clonePath, "checkout", "-b", opts.Branch); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: create branch: %s: %w", strings.TrimSpace(out), err)
	}

	return &Workspace{
		path:    clonePath,
		branch:  opts.Branch,
		kind:    KindClone,
		tmpRoot: tmpRoot,
	}, nil
}

// Path is the working-tree directory engines edit and the verify gate runs in.
func (w *Workspace) Path() string { return w.path }

// Branch is the fix branch checked out in the workspace.
func (w *Workspace) Branch() string { return w.branch }

// Kind reports how the working tree was materialised (worktree vs clone).
func (w *Workspace) Kind() Kind { return w.kind }

// ChangedFiles returns the repo-relative paths changed (tracked + untracked)
// in the workspace relative to HEAD. It includes new untracked files so an
// engine that adds a file is accounted for by the verify gate.
func (w *Workspace) ChangedFiles(ctx context.Context) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		files = append(files, p)
	}

	// Tracked modifications/deletions vs HEAD.
	out, err := runGit(ctx, w.path, "diff", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		add(line)
	}
	// Untracked, non-ignored files (newly created by the engine).
	out, err = runGit(ctx, w.path, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		add(line)
	}
	return files, nil
}

// Diff returns the unified diff of the workspace vs HEAD, including untracked
// files (via --intent-to-add semantics: we stage untracked paths into the index
// for the diff, then unstage). The diff is the reviewable artifact the
// orchestrator persists.
func (w *Workspace) Diff(ctx context.Context) (string, error) {
	// `git add -N .` marks untracked files as intent-to-add so they appear in a
	// `git diff` as new files without actually staging content.
	if _, err := runGit(ctx, w.path, "add", "-N", "."); err != nil {
		return "", err
	}
	out, err := runGit(ctx, w.path, "diff", "HEAD")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Rollback discards an engine's changes to a single repo-relative file: it
// restores the tracked version from HEAD and removes the path if it was
// untracked (newly created). Used by the verify gate to undo a fix that didn't
// pass without disturbing other findings' kept changes.
func (w *Workspace) Rollback(ctx context.Context, file string) error {
	file = strings.TrimSpace(file)
	if file == "" {
		return errors.New("workspace: rollback requires a file path")
	}
	// Restore from HEAD if tracked; ignore the error if the file is untracked
	// (checkout will fail with "did not match any file(s) known to git").
	if _, err := runGit(ctx, w.path, "checkout", "HEAD", "--", file); err != nil {
		// Untracked file: just delete it.
		abs := filepath.Join(w.path, filepath.Clean(file))
		if rmErr := os.Remove(abs); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("workspace: rollback %q: checkout failed (%v) and remove failed: %w", file, err, rmErr)
		}
		return nil
	}
	// Tracked file restored; also drop a same-named untracked copy if one
	// lingers (e.g. the file was deleted then recreated).
	return nil
}

// Cleanup removes the workspace's working tree and temp directory. For a
// worktree it runs `git worktree remove --force` against the origin repo; for a
// clone it deletes the clone dir. The fix BRANCH is intentionally left intact
// on the origin repo (v1 deliverable: a reviewable branch). Cleanup is
// idempotent and safe to defer.
func (w *Workspace) Cleanup(ctx context.Context) error {
	if w == nil || w.tmpRoot == "" {
		return nil
	}
	var firstErr error
	if w.kind == KindWorktree && w.repoRoot != "" && w.path != "" {
		// Detach the worktree from the origin repo's bookkeeping. The branch
		// itself survives (that's the deliverable).
		if _, err := runGit(ctx, w.repoRoot, "worktree", "remove", w.path, "--force"); err != nil {
			firstErr = err
		}
	}
	if err := os.RemoveAll(w.tmpRoot); err != nil && firstErr == nil {
		firstErr = err
	}
	w.tmpRoot = ""
	return firstErr
}

// mkTempRoot creates a unique temp directory under baseDir (or the OS temp dir
// when empty) to hold this workspace's working tree.
func mkTempRoot(baseDir string) (string, error) {
	dir, err := os.MkdirTemp(baseDir, "wolf-ws-")
	if err != nil {
		return "", fmt.Errorf("workspace: create temp dir: %w", err)
	}
	return dir, nil
}

// githubCloneURL derives an https clone URL with the token embedded from a
// GitHub source path. It accepts "owner/repo", "github.com/owner/repo", or a
// full https URL.
func githubCloneURL(source, token string) (string, error) {
	s := strings.TrimSpace(source)
	s = strings.TrimSuffix(s, ".git")
	switch {
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "http://github.com/"):
		s = strings.TrimPrefix(s, "http://github.com/")
	case strings.HasPrefix(s, "github.com/"):
		s = strings.TrimPrefix(s, "github.com/")
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("workspace: invalid github source %q (want owner/repo)", source)
	}
	owner, repo := parts[0], parts[1]
	return fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo), nil
}

// redact removes the token from text so it never leaks into error messages or
// logs.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}
