// Package writability implements the autonomous fix engine's writability
// preflight: before a fix job runs (and surfaced as a repo's derived "fixable"
// indicator in the UI), it answers "can wolf actually write a fix branch to
// this repo's source?" with a clear yes/no + reason.
//
// The three source kinds get three checks (design §8):
//
//   - local  — the path is writable (W_OK) and is a git work tree.
//   - github — a github_token secret exists, the token can push to the repo,
//     and the repo isn't archived.
//   - ssh    — the node is reachable and `git push --dry-run` over the node
//     succeeds against the remote work tree.
//
// All three real probes (filesystem access, the GitHub API, the SSH runner) are
// reached through the Probes interface so tests can stub them with no real
// network / filesystem / docker. Check never panics and never blocks
// indefinitely — every probe is expected to honor the passed context.
package writability

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	repocache "github.com/alphabravocompany/thewolf/internal/repo"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// Result is the derived fixable verdict for a repo. It is intentionally tiny so
// it can be embedded on the repo API response and rendered as a single
// indicator (yes | no — reason) in the UI.
type Result struct {
	Writable bool   `json:"writable"`
	CanPush  bool   `json:"can_push"`
	Reason   string `json:"reason"`
}

// LocalProbe answers "is this local path writable and a git work tree?". The
// production implementation uses unix.Access(W_OK) + a .git check; tests stub
// it. Err is reserved for unexpected I/O failures; a non-writable path is a
// normal (writable=false, reason) answer, not an error.
type LocalProbe interface {
	Check(path string) (writable bool, reason string, err error)
}

// GitHubPushInfo is the slice of a GitHub repo's metadata the preflight needs:
// whether the token has push permission and whether the repo is archived.
type GitHubPushInfo struct {
	CanPush  bool
	Archived bool
}

// GitHubProbe asks the GitHub API whether the given token can push to
// owner/repo (and whether the repo is archived). The production implementation
// calls the REST API; tests stub it.
type GitHubProbe interface {
	PushInfo(ctx context.Context, token, owner, repo string) (GitHubPushInfo, error)
}

// SSHProbe runs `git push --dry-run` against the remote work tree over the
// node and reports whether the push would succeed. The production
// implementation drives the SSH runner; tests stub it.
type SSHProbe interface {
	CanPush(ctx context.Context, node *models.RemoteNode, remotePath string) (ok bool, reason string, err error)
}

// Probes bundles the three injectable probes. A zero Probes (all-nil) makes
// Check return a clear "probe not configured" reason rather than panicking, so
// the surface degrades safely if wired up incompletely.
type Probes struct {
	Local  LocalProbe
	GitHub GitHubProbe
	SSH    SSHProbe

	// ParseGitHubSource splits a github source string into owner/repo. It is a
	// field (not a hard import) so tests can inject a trivial splitter and the
	// package stays free of the scantarget dependency. Production wiring passes
	// scantarget.ParseGitHubSource.
	ParseGitHubSource func(raw string) (owner, repo string, err error)
}

// Check returns the writability verdict for repo. store is used to resolve the
// repo's github_token secret (GitHub) and remote node (SSH). It never returns
// an error: every failure mode is folded into a (writable=false, reason)
// Result so callers always have something to surface.
func Check(ctx context.Context, repo *models.Repo, store db.Store, probes Probes) Result {
	if repo == nil {
		return Result{Writable: false, Reason: "repo not found"}
	}
	switch repo.SourceType {
	case models.SourceTypeLocal:
		return checkLocal(repo, probes)
	case models.SourceTypeGitHub:
		return checkGitHub(ctx, repo, store, probes)
	case models.SourceTypeSSH:
		return checkSSH(ctx, repo, store, probes)
	default:
		return Result{
			Writable: false,
			Reason:   fmt.Sprintf("source type %q is not writable in this version", repo.SourceType),
		}
	}
}

func checkLocal(repo *models.Repo, probes Probes) Result {
	if probes.Local == nil {
		return Result{Writable: false, Reason: "local writability probe not configured"}
	}
	writable, reason, err := probes.Local.Check(repo.SourcePath)
	if err != nil {
		return Result{Writable: false, Reason: "local path check failed: " + err.Error()}
	}
	if !writable {
		if reason == "" {
			reason = "local path is not writable"
		}
		return Result{Writable: false, CanPush: false, Reason: reason}
	}
	return Result{Writable: true, CanPush: true, Reason: "local git work tree is writable"}
}

func checkGitHub(ctx context.Context, repo *models.Repo, store db.Store, probes Probes) Result {
	if probes.GitHub == nil || probes.ParseGitHubSource == nil {
		return Result{Writable: false, Reason: "github writability probe not configured"}
	}
	owner, name, err := probes.ParseGitHubSource(repo.SourcePath)
	if err != nil {
		return Result{Writable: false, Reason: "invalid github source: " + err.Error()}
	}
	token, reason := githubToken(ctx, store, repo)
	if token == "" {
		if local := localGitHubClone(repo, probes); local != "" {
			return Result{
				Writable: true,
				CanPush:  false,
				Reason:   "using the local scan clone; add a working GitHub token to push the fix branch (" + reason + ")",
			}
		}
		return Result{Writable: false, Reason: reason}
	}
	info, err := probes.GitHub.PushInfo(ctx, token, owner, name)
	if err != nil {
		return Result{Writable: false, Reason: "github permission probe failed: " + err.Error()}
	}
	if info.Archived {
		return Result{Writable: false, Reason: "github repository is archived"}
	}
	if !info.CanPush {
		return Result{
			Writable: true,
			CanPush:  false,
			Reason:   "token can clone this repository but lacks push permission (add a write PAT to push the fix branch)",
		}
	}
	return Result{Writable: true, CanPush: true, Reason: "github token can push to this repository"}
}

func checkSSH(ctx context.Context, repo *models.Repo, store db.Store, probes Probes) Result {
	if probes.SSH == nil {
		return Result{Writable: false, Reason: "ssh writability probe not configured"}
	}
	if repo.RemoteNodeID == nil || *repo.RemoteNodeID == "" {
		return Result{Writable: false, Reason: "ssh repo has no remote node configured"}
	}
	node, err := store.GetRemoteNodeByID(ctx, *repo.RemoteNodeID)
	if err != nil {
		return Result{Writable: false, Reason: "remote node not found"}
	}
	remotePath := repo.RemotePath
	if remotePath == "" {
		remotePath = repo.SourcePath
	}
	ok, reason, err := probes.SSH.CanPush(ctx, node, remotePath)
	if err != nil {
		return Result{Writable: false, Reason: "ssh push probe failed: " + err.Error()}
	}
	if !ok {
		if reason == "" {
			reason = "remote git push --dry-run was rejected"
		}
		return Result{Writable: false, Reason: reason}
	}
	return Result{Writable: true, CanPush: true, Reason: "remote work tree accepts a git push"}
}

// githubToken prefers the repo's selected credential, then the user's first
// github_token secret.
func githubToken(ctx context.Context, store db.Store, repo *models.Repo) (token, reason string) {
	if store == nil || repo == nil {
		return "", "no github_token secret available"
	}
	if id := strings.TrimSpace(repo.CredentialSecretID); id != "" {
		sec, err := store.GetSecretByID(ctx, id)
		if err != nil || sec == nil {
			return "", "selected GitHub token was not found"
		}
		if sec.UserID != "" && repo.UserID != "" && sec.UserID != repo.UserID {
			return "", "selected GitHub token does not belong to this repo's owner"
		}
		if sec.KeyType != models.KeyTypeGitHubToken {
			return "", "selected credential is not a github_token"
		}
		plaintext, derr := secrets.Decrypt(sec.EncryptedValue)
		if derr == nil && plaintext != "" {
			return plaintext, ""
		}
		// Selected secret is unreadable (wrong master key, corrupt value).
		// Fall through to any other github_token the user owns.
	}
	if repo.UserID == "" {
		return "", "no github_token secret available"
	}
	list, err := store.ListSecretsByUser(ctx, repo.UserID)
	if err != nil {
		return "", "failed to load secrets"
	}
	for _, s := range list {
		if s.KeyType != models.KeyTypeGitHubToken {
			continue
		}
		plaintext, derr := secrets.Decrypt(s.EncryptedValue)
		if derr != nil {
			return "", "failed to decrypt github_token secret"
		}
		if plaintext != "" {
			return plaintext, ""
		}
	}
	return "", "no github_token secret available"
}

func localGitHubClone(repo *models.Repo, probes Probes) string {
	if repo == nil || probes.ParseGitHubSource == nil {
		return ""
	}
	owner, name, err := probes.ParseGitHubSource(repo.SourcePath)
	if err != nil {
		return ""
	}
	path := repocache.CachedGitHubPath(owner, name)
	if path == "" {
		return ""
	}
	if probes.Local != nil {
		ok, _, err := probes.Local.Check(path)
		if err != nil || !ok {
			return ""
		}
	}
	return path
}
