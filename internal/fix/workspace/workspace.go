// Package workspace prepares a writable working tree on a NEW fix branch for
// the autonomous fix engine, isolated from the user's working tree (design §9).
//
// A Workspace is "where an engine makes its changes." The orchestrator
// (Phase 5) prepares one per job, hands its Path to the engine + verification
// gate, then either keeps the resulting branch (v1: branch-only, no push) or
// rolls it back. The user's original work tree is never touched: local repos
// get a dedicated `git worktree` on a fresh branch; GitHub repos get a
// clone-for-write with a temporary git askpass helper for the token.
//
//   - local  → `git worktree add <tmp> -b <branch>` off the repo's checkout.
//   - github → `git clone <https-url>` into a tmp dir, then a new branch.
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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/models"
	repocache "github.com/alphabravocompany/thewolf/internal/repo"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
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
	// BaseBranch is the scan's source branch. Empty preserves the checkout HEAD.
	BaseBranch string
	// Token is the forge token (GitHub or GitLab) used for a clone-for-write.
	// Required for github, git and gitlab repos, ignored otherwise.
	Token string
	// CloneURL overrides the derived clone URL. When empty it is derived from
	// Repo.SourcePath: https://github.com/<owner>/<repo>.git for GitHub, the
	// validated https URL for git/gitlab sources. Mainly a test seam.
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
	// token is the forge token used for clone/push. Empty for local.
	token string
	// username is the HTTPS username paired with token (forge specific).
	username string
	// defaultBranch is the repo's default branch; Push refuses to push it.
	defaultBranch string
	// verificationBase pins the pre-agent commit across commits and restarts.
	verificationBase string
	// rollbackBase pins the last accepted commit while an agent is working.
	rollbackBase string
}

// runGit runs `git` with args in dir and returns stdout (diagnostics on error). It is a
// package var so tests can stub git entirely; the default shells out.
var runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- "git" is a fixed binary; args are internal (branch names,
	// repo paths), never raw user input.
	cmd := gitpkg.Command(ctx, nil, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out = append(out, exit.Stderr...)
		}
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func runGitWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	// #nosec G204 -- "git" is a fixed binary; args are internal (branch names,
	// repo paths), never raw user input.
	cmd := gitpkg.Command(ctx, env, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out = append(out, exit.Stderr...)
		}
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func cachedGitHubWorktree(repo *models.Repo) string {
	if repo == nil {
		return ""
	}
	owner, name, err := scantarget.ParseGitHubSource(repo.SourcePath)
	if err != nil {
		return ""
	}
	return repocache.CachedGitHubPath(owner, name)
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
	case models.SourceTypeGitHub, models.SourceTypeGit, models.SourceTypeGitLab:
		return prepareClone(ctx, opts)
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

	base, err := baseCommit(ctx, repoRoot, opts.BaseBranch, false)
	if err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, err
	}
	// Resumes must use Open with retained metadata; never adopt an unrelated
	// existing branch when fresh branch creation fails.
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "-b", opts.Branch, wtPath, base); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: create new fix worktree: %w", err)
	}

	ws := &Workspace{
		path:          wtPath,
		branch:        opts.Branch,
		kind:          KindWorktree,
		repoRoot:      repoRoot,
		tmpRoot:       tmpRoot,
		defaultBranch: defaultBranchOf(opts.Repo),
	}
	if err := ws.pinVerificationBase(ctx, true); err != nil {
		_ = ws.Cleanup(ctx)
		return nil, err
	}
	return ws, nil
}

// tokenUsername is the HTTPS username each forge expects alongside a token.
func tokenUsername(repo *models.Repo) string {
	if repo != nil && (repo.SourceType == models.SourceTypeGit || repo.SourceType == models.SourceTypeGitLab) {
		return "oauth2"
	}
	return "x-access-token"
}

func prepareClone(ctx context.Context, opts Options) (*Workspace, error) {
	if opts.Token == "" {
		if cache := cachedGitHubWorktree(opts.Repo); cache != "" {
			local := *opts.Repo
			local.SourceType = models.SourceTypeLocal
			local.SourcePath = cache
			opts.Repo = &local
			return prepareLocal(ctx, opts)
		}
		return nil, errors.New("workspace: write-clone requires a token")
	}
	cloneURL := opts.CloneURL
	if cloneURL == "" {
		derived, err := cloneURLFor(ctx, opts.Repo)
		if err != nil {
			return nil, err
		}
		cloneURL = derived
	}
	username := tokenUsername(opts.Repo)
	tmpRoot, err := mkTempRoot(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	clonePath := filepath.Join(tmpRoot, "clone")

	env, cleanup, err := tokenAuthEnv(tmpRoot, username, opts.Token)
	if err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, err
	}
	defer cleanup()

	if _, err := runGitWithEnv(ctx, "", env, "clone", cloneURL, clonePath); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: clone-for-write: %s", redact(err.Error(), opts.Token))
	}
	base, err := baseCommit(ctx, clonePath, opts.BaseBranch, true)
	if err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, err
	}
	if _, err := runGit(ctx, clonePath, "checkout", "-b", opts.Branch, base); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return nil, fmt.Errorf("workspace: create new fix branch: %w", err)
	}

	ws := &Workspace{
		path:          clonePath,
		branch:        opts.Branch,
		kind:          KindClone,
		tmpRoot:       tmpRoot,
		token:         opts.Token,
		username:      username,
		defaultBranch: defaultBranchOf(opts.Repo),
	}
	if err := ws.pinVerificationBase(ctx, true); err != nil {
		_ = ws.Cleanup(ctx)
		return nil, err
	}
	return ws, nil
}

func baseCommit(ctx context.Context, path, branch string, remote bool) (string, error) {
	args := []string{"rev-parse", "--verify", "HEAD^{commit}"}
	if branch != "" {
		ref := "refs/heads/" + branch
		if remote {
			ref = "refs/remotes/origin/" + branch
		}
		args = []string{"show-ref", "--verify", "--hash", ref}
	}
	commit, err := runGit(ctx, path, args...)
	if err != nil && branch != "" && !remote {
		commit, err = runGit(ctx, path, "show-ref", "--verify", "--hash", "refs/remotes/origin/"+branch)
	}
	if err != nil {
		return "", fmt.Errorf("workspace: source branch %q is unavailable; fetch it before retrying: %w", branch, err)
	}
	return strings.TrimSpace(commit), nil
}

const workspaceMetaName = "wolf-workspace.json"

type workspaceMeta struct {
	Path             string `json:"path"`
	Branch           string `json:"branch"`
	Kind             Kind   `json:"kind"`
	RepoRoot         string `json:"repo_root,omitempty"`
	TmpRoot          string `json:"tmp_root"`
	DefaultBranch    string `json:"default_branch,omitempty"`
	Username         string `json:"username,omitempty"`
	VerificationBase string `json:"verification_base,omitempty"`
	RollbackBase     string `json:"rollback_base,omitempty"`
}

func defaultBranchOf(repo *models.Repo) string {
	if repo == nil {
		return "main"
	}
	if b := strings.TrimSpace(repo.DefaultBranch); b != "" {
		return b
	}
	return "main"
}

func writeMeta(w *Workspace) error {
	if w == nil || w.tmpRoot == "" {
		return nil
	}
	data, err := json.Marshal(workspaceMeta{
		Path: w.path, Branch: w.branch, Kind: w.kind,
		RepoRoot: w.repoRoot, TmpRoot: w.tmpRoot, DefaultBranch: w.defaultBranch, Username: w.username,
		VerificationBase: w.verificationBase,
		RollbackBase:     w.rollbackBase,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.tmpRoot, workspaceMetaName), data, 0o600)
}

// Open rehydrates a workspace previously written by Prepare. token is required
// to push a GitHub clone; it is not persisted on disk.
func Open(path, token string) (*Workspace, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("workspace: open requires a path")
	}
	tmpRoot := path
	metaPath := filepath.Join(path, workspaceMetaName)
	if _, err := os.Stat(metaPath); err != nil {
		// Caller may pass the worktree/clone path; try the parent.
		parent := filepath.Dir(path)
		if _, perr := os.Stat(filepath.Join(parent, workspaceMetaName)); perr == nil {
			tmpRoot = parent
			metaPath = filepath.Join(parent, workspaceMetaName)
		} else {
			return nil, fmt.Errorf("workspace: no metadata at %s", path)
		}
	}
	data, err := os.ReadFile(metaPath) // #nosec G304
	if err != nil {
		return nil, err
	}
	var meta workspaceMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("workspace: parse metadata: %w", err)
	}
	return &Workspace{
		path:             meta.Path,
		branch:           meta.Branch,
		kind:             meta.Kind,
		repoRoot:         meta.RepoRoot,
		tmpRoot:          tmpRoot,
		token:            token,
		username:         meta.Username,
		defaultBranch:    meta.DefaultBranch,
		verificationBase: meta.VerificationBase,
		rollbackBase:     meta.RollbackBase,
	}, nil
}

// Commit stages and commits every change on the fix branch. Identity is forced
// so a worker container without git config still produces a real commit.
func (w *Workspace) Commit(ctx context.Context, message string) error {
	if w == nil || w.path == "" {
		return errors.New("workspace: commit requires a prepared workspace")
	}
	if strings.TrimSpace(message) == "" {
		message = "wolf: apply verified fix"
	}
	if _, err := runGit(ctx, w.path, "add", "-A"); err != nil {
		return fmt.Errorf("workspace: git add: %w", err)
	}
	// Empty commit is a no-op success — the caller already verified a keep.
	if out, err := runGit(ctx, w.path, "status", "--porcelain"); err == nil && strings.TrimSpace(out) == "" {
		return w.acceptCommit()
	}
	if _, err := runGit(ctx, w.path,
		"-c", "user.email=wolf@localhost",
		"-c", "user.name=Wolf Autofix",
		"commit", "-m", message,
	); err != nil {
		return fmt.Errorf("workspace: git commit: %w", err)
	}
	return w.acceptCommit()
}

func (w *Workspace) acceptCommit() error {
	w.rollbackBase = ""
	return writeMeta(w)
}

// Checkpoint preserves the accepted HEAD before agents can create commits.
// Reopening an interrupted attempt must retain its original rollback target.
func (w *Workspace) Checkpoint(ctx context.Context) error {
	if w.rollbackBase == "" {
		base, err := runGit(ctx, w.path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return err
		}
		w.rollbackBase = strings.TrimSpace(base)
	}
	if !validCommitID(w.rollbackBase) {
		return errors.New("workspace: invalid rollback base commit")
	}
	return writeMeta(w)
}

// Push publishes the fix branch to origin. It refuses to push the repo's
// default branch (or main/master) so a misconfigured job cannot land on the
// protected line.
func (w *Workspace) Push(ctx context.Context) (sha string, err error) {
	defer func() {
		if err != nil && w != nil && w.token != "" && strings.Contains(err.Error(), w.token) {
			err = errors.New(redact(err.Error(), w.token))
		}
	}()
	if w == nil || w.path == "" {
		return "", errors.New("workspace: push requires a prepared workspace")
	}
	if protectedBranch(w.branch, w.defaultBranch) {
		return "", fmt.Errorf("workspace: refusing to push protected branch %q", w.branch)
	}
	var env []string
	var cleanup func()
	if w.token != "" {
		var cerr error
		env, cleanup, cerr = tokenAuthEnv(w.tmpRoot, w.username, w.token)
		if cerr != nil {
			return "", cerr
		}
		defer cleanup()
	}
	if len(env) > 0 {
		if _, err := runGitWithEnv(ctx, w.path, env, "push", "-u", "origin", w.branch); err != nil {
			return "", fmt.Errorf("workspace: git push: %w", err)
		}
	} else if _, err := runGit(ctx, w.path, "push", "-u", "origin", w.branch); err != nil {
		return "", fmt.Errorf("workspace: git push: %w", err)
	}
	out, err := runGit(ctx, w.path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func protectedBranch(branch, defaultBranch string) bool {
	b := strings.TrimSpace(strings.ToLower(branch))
	if b == "" || b == "main" || b == "master" {
		return true
	}
	return defaultBranch != "" && strings.EqualFold(branch, defaultBranch)
}

// Path is the working-tree directory engines edit and the verify gate runs in.
func (w *Workspace) Path() string { return w.path }

// Branch is the fix branch checked out in the workspace.
func (w *Workspace) Branch() string { return w.branch }

func (w *Workspace) Head(ctx context.Context) (string, error) {
	head, err := runGit(ctx, w.path, "rev-parse", "--verify", "HEAD^{commit}")
	return strings.TrimSpace(head), err
}

// Kind reports how the working tree was materialised (worktree vs clone).
func (w *Workspace) Kind() Kind { return w.kind }

// ChangedFiles returns the repo-relative paths changed (tracked + untracked)
// in the workspace relative to HEAD. It includes new untracked files so an
// engine that adds a file is accounted for by the verify gate.
func (w *Workspace) ChangedFiles(ctx context.Context) ([]string, error) {
	return w.changedFilesFrom(ctx, "HEAD")
}

// BranchChangedFiles includes agent-created commits and staged/untracked edits.
// Old retained workspaces pin their fork point once before further agent work.
func (w *Workspace) BranchChangedFiles(ctx context.Context) ([]string, error) {
	base, err := w.verificationCommit(ctx)
	if err != nil {
		return nil, err
	}
	return w.changedFilesFrom(ctx, base)
}

func validCommitID(id string) bool {
	_, err := hex.DecodeString(id)
	return err == nil && (len(id) == 40 || len(id) == 64)
}

func (w *Workspace) verificationCommit(ctx context.Context) (string, error) {
	if w.verificationBase == "" {
		if err := w.pinVerificationBase(ctx, false); err != nil {
			return "", err
		}
	}
	if !validCommitID(w.verificationBase) {
		return "", errors.New("workspace: invalid verification base commit")
	}
	return w.verificationBase, nil
}

func (w *Workspace) pinVerificationBase(ctx context.Context, createdBranch bool) error {
	var base string
	var err error
	if createdBranch {
		base, err = runGit(ctx, w.path, "rev-parse", "--verify", "HEAD^{commit}")
	} else {
		if w.defaultBranch == "" {
			return errors.New("workspace: retained branch has no verification baseline or default branch")
		}
		base, err = runGit(ctx, w.path, "merge-base", "HEAD", "refs/heads/"+w.defaultBranch)
		if err != nil {
			base, err = runGit(ctx, w.path, "merge-base", "HEAD", "refs/remotes/origin/"+w.defaultBranch)
		}
	}
	if err != nil {
		return fmt.Errorf("workspace: pin verification baseline: %w", err)
	}
	w.verificationBase = strings.TrimSpace(base)
	return writeMeta(w)
}

func (w *Workspace) changedFilesFrom(ctx context.Context, base string) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		files = append(files, p)
	}

	// Tracked modifications/deletions from the requested commit to disk.
	out, err := runGit(ctx, w.path, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", base, "--")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\x00") {
		add(line)
	}
	// Untracked, non-ignored files (newly created by the engine).
	out, err = runGit(ctx, w.path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\x00") {
		add(line)
	}
	return files, nil
}

// Diff includes committed and uncommitted changes from the pinned branch base.
// Untracked files enter the index as intent-to-add so they appear in the diff.
func (w *Workspace) Diff(ctx context.Context) (string, error) {
	base, err := w.verificationCommit(ctx)
	if err != nil {
		return "", err
	}
	// `git add -N .` marks untracked files as intent-to-add so they appear in a
	// `git diff` as new files without actually staging content.
	if _, err := runGit(ctx, w.path, "add", "-N", "."); err != nil {
		return "", err
	}
	out, err := runGit(ctx, w.path, "diff", "--no-ext-diff", "--no-textconv", base, "--")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Rollback discards an engine's changes to a single repo-relative file: it
// restores the checkpoint version (HEAD outside an agent attempt) and removes
// newly created paths. If the agent committed the rejected edit, a path-only
// revert commit also removes it from the branch without rewriting history.
func (w *Workspace) Rollback(ctx context.Context, file string) error {
	if !filepath.IsLocal(file) || filepath.Clean(file) == "." {
		return errors.New("workspace: rollback requires a local file path")
	}
	file = filepath.Clean(file)
	for _, segment := range strings.Split(file, string(filepath.Separator)) {
		if strings.EqualFold(segment, ".git") {
			return errors.New("workspace: rollback cannot modify Git metadata")
		}
	}
	base := "HEAD"
	if w.rollbackBase != "" {
		if !validCommitID(w.rollbackBase) {
			return errors.New("workspace: invalid rollback base commit")
		}
		base = w.rollbackBase
	}
	// Determine checkpoint membership independently. A locked index, cancellation, or
	// failed checkout must never be interpreted as permission to delete a file.
	entry, err := runGit(ctx, w.path, "--literal-pathspecs", "ls-tree", "-z", base, "--", file)
	if err != nil {
		return err
	}
	if entry != "" {
		if !strings.Contains(strings.SplitN(entry, "\t", 2)[0], " blob ") {
			return errors.New("workspace: rollback requires a file, not a directory or submodule")
		}
		if _, err = runGit(ctx, w.path, "--literal-pathspecs", "checkout", base, "--", file); err != nil {
			return err
		}
		return w.commitRollback(ctx, file)
	}
	root, err := os.OpenRoot(w.path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && info.IsDir() {
		return errors.New("workspace: rollback cannot remove a directory")
	}
	// Newly staged/intent-to-add files must leave the index as well as disk.
	if _, err := runGit(ctx, w.path, "--literal-pathspecs", "rm", "--cached", "--ignore-unmatch", "--", file); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Remove(file); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace: remove new file %q: %w", file, err)
	}
	return w.commitRollback(ctx, file)
}

func (w *Workspace) commitRollback(ctx context.Context, file string) error {
	if w.rollbackBase == "" {
		return nil
	}
	// Only committed rejected changes require a revert commit. Ordinary dirty
	// rollbacks and unrelated staged files must not be committed here.
	changed, err := runGit(ctx, w.path, "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", w.rollbackBase, "HEAD", "--", file)
	if err != nil || changed == "" {
		return err
	}
	_, err = runGit(ctx, w.path, "--literal-pathspecs", "-c", "user.email=wolf@localhost", "-c", "user.name=Wolf Autofix",
		"commit", "--only", "-m", "wolf: revert rejected agent edit", "--", file)
	return err
}

// Discard removes the workspace and the fix branch. Use this when a job is
// cancelled so leftover wolf-fix/* branches do not sit around waiting to push.
func (w *Workspace) Discard(ctx context.Context) error {
	if w == nil {
		return nil
	}
	branch := w.branch
	root := w.repoRoot
	kind := w.kind
	if err := w.Cleanup(ctx); err != nil {
		return err
	}
	if kind == KindWorktree && root != "" && isDiscardableBranch(branch) {
		return DiscardLocalBranch(ctx, root, branch)
	}
	return nil
}

// DiscardPath rehydrates a workspace at path (worktree or clone) and discards
// it plus its fix branch. Safe if the path is already gone.
func DiscardPath(ctx context.Context, workspacePath, branch string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return nil
	}
	if _, err := os.Lstat(workspacePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	ws, err := Open(workspacePath, "")
	if err != nil {
		// A directory name is not proof of ownership. Missing or unreadable
		// metadata must never authorize recursive deletion.
		return err
	}
	if branch == "" || ws.Branch() != branch {
		return errors.New("workspace: discard branch does not match retained workspace")
	}
	return ws.Discard(ctx)
}

// DiscardLocalBranch deletes a wolf-fix/* branch from a local origin repo
// (the worktree may already be gone). Refuses anything that is not a Wolf
// fix branch.
func DiscardLocalBranch(ctx context.Context, repoPath, branch string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" || !isDiscardableBranch(branch) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := runGit(ctx, repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil // already removed
		}
		return err
	}
	_, err := runGit(ctx, repoPath, "branch", "-D", branch)
	return err
}

func isDiscardableBranch(branch string) bool {
	b := strings.TrimSpace(branch)
	return strings.HasPrefix(b, "wolf-fix/") && !strings.Contains(b, "..")
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.kind == KindWorktree && w.repoRoot != "" && w.path != "" {
		// Detach the worktree from the origin repo's bookkeeping. The branch
		// itself survives (that's the deliverable).
		if _, err := runGit(ctx, w.repoRoot, "worktree", "remove", w.path, "--force"); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(w.tmpRoot); err != nil {
		return err
	}
	w.tmpRoot = ""
	return nil
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

// githubCloneURL derives an https clone URL from a GitHub source path. It
// accepts "owner/repo", "github.com/owner/repo", or a full https URL.
func githubCloneURL(source string) (string, error) {
	owner, repo, err := scantarget.ParseGitHubSource(source)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo), nil
}

// cloneURLFor derives the clone URL for a remote repo. GitHub sources are
// rebuilt from owner/name; git and gitlab sources must be a plain https URL
// that passes the shared Git network boundary (private hosts need
// WOLF_GIT_ALLOWED_CIDRS).
func cloneURLFor(ctx context.Context, repo *models.Repo) (string, error) {
	if repo.SourceType == models.SourceTypeGitHub {
		return githubCloneURL(repo.SourcePath)
	}
	canonical, host, err := scantarget.CanonicalGitURL(repo.SourcePath)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(canonical, "https://") {
		return "", errors.New("workspace: only https Git URLs can be fixed")
	}
	if err := scantarget.ValidateRemoteDestination(ctx, host); err != nil {
		return "", err
	}
	return canonical, nil
}

func tokenAuthEnv(tmpRoot, username, token string) ([]string, func(), error) {
	if username == "" {
		username = "x-access-token"
	}
	askpass := filepath.Join(tmpRoot, "git-askpass.sh")
	body := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"*Username*) printf '%s\\n' " + shellSingleQuote(username) + " ;;\n" +
		"*Password*) printf '%s\\n' " + shellSingleQuote(token) + " ;;\n" +
		"*) printf '\\n' ;;\n" +
		"esac\n"
	if err := os.WriteFile(askpass, []byte(body), 0o700); err != nil {
		return nil, func() {}, fmt.Errorf("workspace: create git askpass helper: %w", err)
	}
	cleanup := func() { _ = os.Remove(askpass) }
	return append([]string{
		"GIT_ASKPASS=" + askpass,
		"GIT_TERMINAL_PROMPT=0",
	}, gitpkg.CAEnv()...), cleanup, nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// redact removes the token from text so it never leaks into error messages or
// logs.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}

// DiscardFixJob removes only the retained job workspace and its local branch.
// Its caller keeps the repository/claim exclusion until cleanup is acknowledged.
func DiscardFixJob(ctx context.Context, job *models.FixJob, repo *models.Repo) error {
	if err := DiscardPath(ctx, job.WorkspacePath, job.ResultBranch); err != nil {
		return err
	}
	if repo != nil && repo.SourceType == models.SourceTypeLocal {
		return DiscardLocalBranch(ctx, repo.SourcePath, job.ResultBranch)
	}
	return nil
}

// ErrNoHandoff means the remediation never produced a branch, so there is nothing to hand off.
var ErrNoHandoff = errors.New("workspace: handoff requires a repository and branch")

// AcceptRemediation removes a clean checkout whose commit survives in the
// origin repository or matches a publication verified by AcceptFixRemediation.
func AcceptRemediation(ctx context.Context, rem *models.Remediation, repo *models.Repo) (string, error) {
	if rem == nil || repo == nil || rem.Branch == "" {
		return "", ErrNoHandoff
	}
	root := repo.SourcePath
	if repo.SourceType != models.SourceTypeLocal {
		root = cachedGitHubWorktree(repo)
	}
	var ws *Workspace
	published := rem.State == models.RemediationFrozen && rem.PublishedSHA != ""
	if rem.WorkspacePath != "" {
		if _, err := os.Lstat(rem.WorkspacePath); err == nil {
			ws, err = Open(rem.WorkspacePath, "")
			if err != nil {
				return "", err
			}
			if ws.kind != KindWorktree && (!published || ws.kind != KindClone) {
				return "", errors.New("workspace: publish this clone before removing its only local branch")
			}
			if ws.branch != rem.Branch || filepath.Clean(ws.Path()) != filepath.Clean(rem.WorkspacePath) {
				return "", errors.New("workspace: handoff branch does not match the retained workspace")
			}
			if ws.kind == KindWorktree {
				expected, err := filepath.Abs(root)
				if err != nil {
					return "", err
				}
				actual, err := filepath.Abs(ws.repoRoot)
				if err != nil || root == "" || actual != expected {
					return "", errors.New("workspace: handoff repository does not match retained workspace")
				}
			}
			changed, err := ws.ChangedFiles(ctx)
			if err != nil {
				return "", err
			}
			if len(changed) != 0 {
				return "", errors.New("workspace: commit or discard local changes before accepting the branch")
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	sha := rem.PublishedSHA
	if !published {
		if root == "" {
			return "", errors.New("workspace: local handoff repository is unavailable")
		}
		var err error
		sha, err = runGit(ctx, root, "rev-parse", "--verify", "refs/heads/"+rem.Branch+"^{commit}")
		if err != nil {
			return "", err
		}
	}
	sha = strings.TrimSpace(sha)
	if ws != nil {
		head, err := runGit(ctx, ws.Path(), "rev-parse", "--verify", "HEAD")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(head) != sha {
			return "", errors.New("workspace: retained checkout does not match the handoff branch")
		}
		if err := ws.Cleanup(ctx); err != nil {
			return "", err
		}
	}
	return sha, nil
}
