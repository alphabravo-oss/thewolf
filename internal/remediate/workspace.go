package remediate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// BranchName is the remediation branch for a session. Deterministic so a
// retried session reuses its branch name rather than accumulating orphans.
func BranchName(sessionID string) string {
	return "wolf/remediation-" + sessionID
}

// stripAgentConfig is a package var so a test can force this specific step
// of prepareWorkspace to fail deterministically — matching the seam pattern
// internal/fix/workspace's own runGit already uses. A permission-based
// filesystem trick doesn't reach this reliably: directory mode bits aren't
// preserved by `git clone`/`git worktree add`, which is exactly why this
// step needs its own seam rather than one further down in a real git call.
var stripAgentConfig = StripAgentConfig

// prepareWorkspace creates the isolated workspace the agent edits, on
// BranchName's deterministic branch, and strips any repo-supplied agent
// config before anything can reach the driver. On success sess.WorktreePath,
// sess.BranchName, and sess.CloneRoot are set so later phases — including a
// resumed session reloaded from the store after a gate pause — find the
// same worktree and (for a local repo) the scratch clone it sits on.
//
// Local repositories are cloned, never worktree-added. Passing a local Repo
// straight through to workspace.Prepare would run workspace.prepareLocal's
// `git worktree add`, whose .git is a FILE pointing at the SOURCE repo's own
// object store — the driver would then have to mount that store read-write
// into the container, handing an agent running under --auto write access to
// the user's real refs and objects, not just to an ephemeral checkout.
// cloneLocalForRemediation instead makes a fresh, self-contained, disposable
// clone with --no-hardlinks (its own object files, not the source's), so
// refs AND the object store are both genuinely isolated: a build step the
// driver runs (make/go test/npm run/pytest are all permission-document
// allowed) can corrupt the clone's own objects without ever touching an
// inode the source repo shares. workspace.Prepare then worktrees off THAT
// clone, never the user's own repository.
//
// Cloning also fixes retry for the `git worktree add -b <branch>` step:
// BranchName is deterministic, and that command fails outright once a branch
// of that name already exists on the repo it runs against. A worktree taken
// directly off the source repo would carry that branch forward from a prior
// attempt and die here on the second try. A fresh clone has never seen that
// branch, so every attempt's worktree add starts clean.
//
// That guarantee stops at worktree add, though. Since Task 13, landing
// pushes this same branch to the ORIGINAL source repo — see
// cloneLocalForRemediation's own doc comment below — so a session retried
// under its own BranchName would now collide one step later than before: not
// at worktree add (a fresh clone still starts clean there), but at the push,
// against the FIRST attempt's already-landed branch on the real origin — and
// only after a full paid agentic run, not before one. Unreachable today (a
// session ID, and so BranchName, is used exactly once; nothing in this
// subsystem retries a session under its own ID), so this is documentation
// only — flagged for whoever changes that.
//
// GitHub-sourced repos already clone-for-write inside workspace.Prepare, so
// they pass through unchanged; sess.CloneRoot stays empty for them, since
// there is no separate scratch-clone layer to track.
func (r *Runner) prepareWorkspace(ctx context.Context, sess *models.RemediationSession) (ws *workspace.Workspace, err error) {
	// Matches runPlanPhase/runExecutePhase's shape: a single defer routing
	// any error through failSession, so a failure here (repo missing, clone
	// failed, strip failed) marks the session failed instead of leaving it
	// stuck in "pending" — the orphaned-row hazard Task 6's review found
	// five instances of.
	defer func() {
		if err != nil {
			r.failSession(ctx, sess, models.RemediationFailed, err)
		}
	}()

	repo, rerr := r.store.GetRepoByID(ctx, sess.RepoID)
	if rerr != nil {
		return nil, fmt.Errorf("load repo: %w", rerr)
	}

	opts := workspace.Options{Repo: repo, Branch: BranchName(sess.ID)}
	var cloneRoot string
	if repo.SourceType == models.SourceTypeLocal {
		cloned, cleanup, cerr := cloneLocalForRemediation(ctx, repo.SourcePath)
		if cerr != nil {
			// cloneLocalForRemediation's own errors are already fully
			// descriptive ("clone local repo: ..."); wrapping again here
			// produced "clone local repo: clone local repo: ...".
			return nil, cerr
		}
		// Past this point the scratch clone must outlive prepareWorkspace:
		// every remaining phase, and eventually landing (push + PR), reuses
		// the worktree built on top of it. Only this function's OWN
		// remaining failure paths tear it down here.
		defer func() {
			if err != nil {
				cleanup()
			}
		}()
		opts.Repo = cloned
		cloneRoot = cloned.SourcePath
	}

	prepared, perr := workspace.Prepare(ctx, opts)
	if perr != nil {
		return nil, fmt.Errorf("prepare workspace: %w", perr)
	}
	// Same reasoning as the clone cleanup above: only a failure in the rest
	// of THIS function tears the worktree down. Deferred after the clone
	// cleanup, so on unwind it runs first — `git worktree remove` still has
	// a live scratch clone to run against.
	defer func() {
		if err != nil {
			_ = prepared.Cleanup(ctx)
		}
	}()

	// A repo-supplied opencode.json overrides the permission document Wolf
	// injects (proven empirically by the spike behind Task 4a) — the
	// scanned repository is untrusted input by definition, so its own agent
	// config must not be allowed to outrank Wolf's before the driver ever
	// runs.
	removed, serr := stripAgentConfig(prepared.Path())
	if serr != nil {
		return nil, fmt.Errorf("strip agent config: %w", serr)
	}

	// Set the session's workspace fields only once every step above has
	// actually succeeded. Setting them earlier would let a later failure in
	// this function (e.g. the strip above) persist paths to directories the
	// defers just deleted — failSession's transition writes whatever sess
	// currently holds, and "a failed session retains its worktree for
	// inspection" must not become a lie about a worktree that no longer
	// exists.
	sess.WorktreePath = prepared.Path()
	sess.BranchName = prepared.Branch()
	sess.CloneRoot = cloneRoot

	for _, path := range removed {
		r.recordEvent(ctx, sess.ID, "worktree.config_stripped", path)
	}
	return prepared, nil
}

// cloneLocalForRemediation makes a disposable, self-contained copy of a
// local repo for prepareWorkspace to worktree off of. See prepareWorkspace's
// doc comment for why a worktree taken directly off the user's real
// checkout is not safe here. The returned cleanup removes the clone; the
// caller owns calling it once the clone — and everything built on top of it
// — is no longer needed.
//
// The clone is cloned with --no-hardlinks (see the git invocation below for
// why: a plain --local clone shares object-file INODES with the source,
// which a plain "disposable directory" story does not protect against).
//
// The clone's `origin` remote points at sourcePath — the user's real
// repository — because that is what `git clone` always records as the
// source it cloned from, regardless of --local. That is fine for what this
// function does (nothing here ever fetches or pushes), but it means a
// future `git push origin <branch>` run from the clone's worktree (Task 13's
// landing phase) pushes into the user's real repo. Left as-is deliberately;
// flagged here so Task 13's author does not discover it by accident.
//
// sourcePath is Repo.SourcePath, which traces back to user-supplied API
// input (createRepoRequest.SourcePath in internal/api/routes/repos.go) that
// is validated only for emptiness — nothing there rejects a leading '-' or
// requires an absolute path (CWE-88, argument injection). Without the "--"
// separator below, a value like "--upload-pack=<command>" is parsed by git
// as a FLAG rather than the path it's supposed to be. Verified empirically
// against this exact call shape (git 2.39.5): a bare leading-dash payload
// here does NOT reach a working command-execution primitive, because the
// flag consumes sourcePath's argv slot and leaves only clonePath (our own
// fresh, not-yet-existing scratch dir) as the sole remaining positional —
// git rejects that as "repository does not exist" before invoking anything.
// The two other classic vectors for this class of bug are also closed on
// this git version independent of these checks: `ext::` transports are
// blocked by git's own protocol allowlist, and `ssh://-oProxyCommand=...`
// hostnames are rejected (the CVE-2017-1000117 fix). None of that is a
// reason to skip validating here: an older/differently-configured git, or a
// call shape that gains a second attacker-influenced positional later,
// could reopen this. The checks below reject the input outright rather than
// leaning on git's or the transport's own defenses holding forever.
func cloneLocalForRemediation(ctx context.Context, sourcePath string) (*models.Repo, func(), error) {
	if strings.TrimSpace(sourcePath) == "" {
		return nil, nil, fmt.Errorf("clone local repo: source path is empty")
	}
	// Belt-and-braces alongside the `--` separator below: fails fast with a
	// clear reason, and keeps protecting even if the argv order downstream
	// is ever changed by someone who doesn't realize `--` was load-bearing.
	if strings.HasPrefix(sourcePath, "-") {
		return nil, nil, fmt.Errorf("clone local repo: source path %q must not start with '-'", sourcePath)
	}
	if !filepath.IsAbs(sourcePath) {
		return nil, nil, fmt.Errorf("clone local repo: source path %q must be absolute", sourcePath)
	}
	tmpRoot, err := os.MkdirTemp("", "wolf-remediate-clone-")
	if err != nil {
		return nil, nil, fmt.Errorf("clone local repo: create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpRoot) }

	clonePath := filepath.Join(tmpRoot, "repo")
	// --local skips the network and the full "Git aware" transport, cloning
	// via a local filesystem copy — cheap, and what makes this call safe to
	// run synchronously in the request/session path. --no-hardlinks forces
	// that copy to be a REAL copy of the object files rather than git's
	// default local-clone optimization of hardlinking them: a hardlinked
	// object file is the SAME inode as the source's, so the driver mounting
	// this clone's .git directory read-write — combined with the execute
	// permission document allowing make/go test/npm run/pytest, i.e.
	// repo-supplied code execution — could write through the link and
	// corrupt the user's real object store. The clone is ephemeral and
	// scratch-sized, so the real-copy cost is bounded and short-lived; that
	// cost is the actual price of the isolation this clone exists to buy.
	// #nosec G204 -- "git" is a fixed binary. sourcePath IS user-supplied
	// (see the doc comment above) — this is safe because of the explicit
	// leading-dash/absolute-path checks above plus the "--" separator here,
	// which together stop sourcePath from ever being parsed as a flag,
	// not because the input is trusted.
	cmd := exec.CommandContext(ctx, "git", "clone", "--local", "--no-hardlinks", "--", sourcePath, clonePath)
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		cleanup()
		return nil, nil, fmt.Errorf("clone local repo: %s: %w", strings.TrimSpace(string(out)), cerr)
	}

	return &models.Repo{
		SourceType: models.SourceTypeLocal,
		SourcePath: clonePath,
	}, cleanup, nil
}
