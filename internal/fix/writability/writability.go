// Package writability implements the autonomous fix engine's writability
// preflight: before a fix job runs (and surfaced as a repo's derived "fixable"
// indicator in the UI), it answers "can wolf actually write a fix branch to
// this repo's source?" with a clear yes/no + reason.
//
// The source kinds get these checks (design §8):
//
//   - local  — the path is writable (W_OK) and is a git work tree.
//   - github — a github_token secret exists, the token can push to the repo,
//     and the repo isn't archived.
//   - gitlab — a git/gitlab source with an https URL and a gitlab_token
//     credential bound to its host (gitlab.com or self-hosted) whose role can
//     push, and the project isn't archived.
//   - ssh    — disabled in the production probes until remote workspace
//     preparation is supported; an injected probe can check a remote node.
//
// All three real probes (filesystem access, the GitHub API, the SSH runner) are
// reached through the Probes interface so tests can stub them with no real
// network / filesystem / docker. Check never panics and never blocks
// indefinitely — every probe is expected to honor the passed context.
package writability

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	repocache "github.com/alphabravocompany/thewolf/internal/repo"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
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
	Check(ctx context.Context, path string) (writable bool, reason string, err error)
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

// GitLabProjectInfo is what the preflight needs to know about a GitLab project.
type GitLabProjectInfo struct {
	CanPush       bool
	Archived      bool
	MergeRequests bool
}

// GitLabProbe asks a GitLab instance what the token can do on a project.
type GitLabProbe interface {
	ProjectInfo(ctx context.Context, host, token, project string) (GitLabProjectInfo, error)
}

// Probes bundles the three injectable probes. A zero Probes (all-nil) makes
// Check return a clear "probe not configured" reason rather than panicking, so
// the surface degrades safely if wired up incompletely.
type Probes struct {
	Local  LocalProbe
	GitHub GitHubProbe
	GitLab GitLabProbe
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
		return checkLocal(ctx, repo, probes)
	case models.SourceTypeGitHub:
		return checkGitHub(ctx, repo, store, probes)
	case models.SourceTypeGit, models.SourceTypeGitLab:
		return checkGitLab(ctx, repo, store, probes)
	case models.SourceTypeSSH:
		return checkSSH(ctx, repo, store, probes)
	default:
		return Result{
			Writable: false,
			Reason:   fmt.Sprintf("source type %q is not writable in this version", repo.SourceType),
		}
	}
}

func checkLocal(ctx context.Context, repo *models.Repo, probes Probes) Result {
	if probes.Local == nil {
		return Result{Writable: false, Reason: "local writability probe not configured"}
	}
	writable, reason, err := probes.Local.Check(ctx, repo.SourcePath)
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
	token, reason := GitHubToken(ctx, store, repo)
	if token == "" {
		if repo.CredentialSecretID != "" {
			return Result{Reason: reason}
		}
		if local := localGitHubClone(ctx, repo, probes); local != "" {
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
		return Result{Writable: false, Reason: "github permission probe failed: " + strings.ReplaceAll(err.Error(), token, "[REDACTED]")}
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

func checkGitLab(ctx context.Context, repo *models.Repo, store db.Store, probes Probes) Result {
	if probes.GitLab == nil {
		return Result{Reason: "gitlab writability probe not configured"}
	}
	host, project, err := scantarget.ParseGitLabSource(repo.SourcePath)
	if err != nil {
		return Result{Reason: "only https GitLab project URLs can be fixed: " + err.Error()}
	}
	token, reason := GitLabToken(ctx, store, repo)
	if token == "" {
		return Result{Reason: reason}
	}
	info, err := probes.GitLab.ProjectInfo(ctx, host, token, project)
	if err != nil {
		return Result{Reason: "gitlab permission probe failed: " + strings.ReplaceAll(err.Error(), token, "[REDACTED]")}
	}
	switch {
	case info.Archived:
		return Result{Reason: "gitlab project is archived"}
	case !info.CanPush:
		return Result{Writable: true, Reason: "token can read this project but its role is below Developer; use a token with the api and write_repository scopes and at least the Developer role"}
	case !info.MergeRequests:
		return Result{Writable: true, Reason: "merge requests are disabled for this GitLab project"}
	}
	return Result{Writable: true, CanPush: true, Reason: "gitlab token can push to this project"}
}

func checkSSH(ctx context.Context, repo *models.Repo, store db.Store, probes Probes) Result {
	if probes.SSH == nil {
		return Result{Writable: false, Reason: "SSH remediation is not supported by this worker; register a local clone or GitHub source to fix this repository"}
	}
	if repo.RemoteNodeID == nil || *repo.RemoteNodeID == "" {
		return Result{Writable: false, Reason: "ssh repo has no remote node configured"}
	}
	if store == nil {
		return Result{Reason: "remote node store is unavailable"}
	}
	node, err := store.GetRemoteNodeByID(ctx, *repo.RemoteNodeID)
	if err != nil || node == nil {
		return Result{Writable: false, Reason: "remote node not found"}
	}
	if repo.UserID == "" || node.UserID != repo.UserID {
		return Result{Reason: "remote node does not belong to this repo's owner"}
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

// GitHubToken uses the repo's selected credential, or the owner's first
// github_token when no credential is selected. A broken selection fails closed.
func GitHubToken(ctx context.Context, store db.Store, repo *models.Repo) (token, reason string) {
	if store == nil || repo == nil || repo.UserID == "" {
		return "", "no github_token secret available"
	}
	if id := strings.TrimSpace(repo.CredentialSecretID); id != "" {
		sec, err := store.GetSecretByID(ctx, id)
		if err != nil || sec == nil {
			return "", "selected GitHub token was not found"
		}
		if sec.UserID != repo.UserID {
			return "", "selected GitHub token does not belong to this repo's owner"
		}
		if sec.KeyType != models.KeyTypeGitHubToken {
			return "", "selected credential is not a github_token"
		}
		plaintext, derr := secrets.Decrypt(sec.EncryptedValue)
		if derr == nil && plaintext != "" {
			return plaintext, ""
		}
		return "", "selected GitHub token is unreadable or empty; replace it in Account → Secrets"
	}
	list, err := store.ListSecretsByUser(ctx, repo.UserID)
	if err != nil {
		return "", "failed to load secrets"
	}
	for _, s := range list {
		if s.KeyType != models.KeyTypeGitHubToken || s.UserID != repo.UserID {
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

func localGitHubClone(ctx context.Context, repo *models.Repo, probes Probes) string {
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
		ok, _, err := probes.Local.Check(ctx, path)
		if err != nil || !ok {
			return ""
		}
	}
	return path
}

// GitLabToken returns the repo's selected gitlab_token. It is never guessed:
// the credential must be bound (allowed_hosts) to the repo URL's host so a
// token cannot be sent to a different GitLab instance.
func GitLabToken(ctx context.Context, store db.Store, repo *models.Repo) (token, reason string) {
	if store == nil || repo == nil || repo.UserID == "" {
		return "", "no gitlab_token credential available"
	}
	id := strings.TrimSpace(repo.CredentialSecretID)
	if id == "" {
		return "", "select a gitlab_token credential on this repo (Account → Secrets) to fix a GitLab project"
	}
	host, _, err := scantarget.ParseGitLabSource(repo.SourcePath)
	if err != nil {
		return "", err.Error()
	}
	sec, err := store.GetSecretByID(ctx, id)
	if err != nil || sec == nil {
		return "", "selected GitLab credential was not found"
	}
	if sec.UserID != repo.UserID {
		return "", "selected GitLab credential does not belong to this repo's owner"
	}
	if sec.KeyType != models.KeyTypeGitLabToken {
		return "", "selected credential is not a gitlab_token"
	}
	hostname := hostOnly(host)
	if !scantarget.CredentialAllowsHost(sec, hostname) {
		return "", "selected GitLab credential is not allowed for host " + hostname
	}
	plaintext, derr := secrets.Decrypt(sec.EncryptedValue)
	if derr != nil || plaintext == "" {
		return "", "selected GitLab token is unreadable or empty; replace it in Account → Secrets"
	}
	return plaintext, ""
}

// hostOnly drops the port from a host[:port] string.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// ForgeToken returns the publishing token for the repo's source type.
func ForgeToken(ctx context.Context, store db.Store, repo *models.Repo) (token, reason string) {
	if repo != nil && (repo.SourceType == models.SourceTypeGit || repo.SourceType == models.SourceTypeGitLab) {
		return GitLabToken(ctx, store, repo)
	}
	return GitHubToken(ctx, store, repo)
}
