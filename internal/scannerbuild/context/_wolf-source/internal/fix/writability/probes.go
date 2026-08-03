package writability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/github"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remote"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

// DefaultProbes returns the production wiring of the three probes for the given
// store. Tests construct Probes directly with stubs instead of calling this.
func DefaultProbes(store db.Store) Probes {
	return Probes{
		Local:             localFSProbe{},
		GitHub:            githubAPIProbe{},
		SSH:               sshRunnerProbe{store: store},
		ParseGitHubSource: scantarget.ParseGitHubSource,
	}
}

// localFSProbe checks a local path with unix.Access(W_OK) and confirms it is a
// git work tree (a .git entry — directory for a normal clone, file for a
// worktree/submodule).
type localFSProbe struct{}

func (localFSProbe) Check(path string) (bool, string, error) {
	if path == "" {
		return false, "local source path is empty", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "local path does not exist", nil
		}
		return false, "", err
	}
	if !info.IsDir() {
		return false, "local path is not a directory", nil
	}
	if err := unix.Access(path, unix.W_OK); err != nil {
		return false, "local path is not writable", nil
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false, "local path is not a git work tree", nil
	}
	return true, "", nil
}

// githubAPIProbe asks the GitHub REST API whether the token can push to
// owner/repo and whether the repo is archived.
type githubAPIProbe struct{}

func (githubAPIProbe) PushInfo(ctx context.Context, token, owner, repo string) (GitHubPushInfo, error) {
	info, err := github.New(token).RepoPushInfo(ctx, owner, repo)
	if err != nil {
		return GitHubPushInfo{}, err
	}
	return GitHubPushInfo{CanPush: info.CanPush, Archived: info.Archived}, nil
}

// sshRunnerProbe runs `git push --dry-run` against the remote work tree over
// the node's SSH connection. A clean exit means the push would succeed.
type sshRunnerProbe struct {
	store  db.Store
	runner sshclient.Runner // nil → real client (via remote.Service)
}

func (p sshRunnerProbe) CanPush(ctx context.Context, node *models.RemoteNode, remotePath string) (bool, string, error) {
	svc := remote.Service{Store: p.store, Runner: p.runner}
	cfg, err := svc.ConfigForNode(ctx, node)
	if err != nil {
		return false, "", err
	}
	runner := p.runner
	if runner == nil {
		runner = sshclient.Client{}
	}
	// Reachability + writability + a dry-run push, all in one round trip. The
	// dry-run never mutates the remote; a non-zero exit (rejected push,
	// read-only remote, missing repo) surfaces as a clear reason.
	cmd := `repo=` + sshclient.ShellQuote(remotePath) + `;
if [ ! -d "$repo" ]; then echo "remote path does not exist"; exit 44; fi
cd "$repo" || { echo "cannot enter remote path"; exit 44; }
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "remote path is not a git work tree"; exit 45; fi
if [ ! -w "$repo" ]; then echo "remote work tree is not writable"; exit 46; fi
git push --dry-run 2>&1`
	res, err := runner.Run(ctx, cfg, cmd)
	if err != nil {
		reason := res.Stdout
		if reason == "" {
			reason = res.Stderr
		}
		if reason == "" {
			reason = err.Error()
		}
		return false, fmt.Sprintf("remote push probe rejected: %s", reason), nil
	}
	return true, "", nil
}
