package scantarget

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remote"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

type Prepared struct {
	Path              string
	SourceType        models.SourceType
	RemoteNodeID      *string
	SourcePath        string
	CommitSHA         string
	DirtyState        string
	PreparedWorkspace string
	Cleanup           func()
}

// GitHubCloner clones (or refreshes) a GitHub repository to a local path,
// returning that path. Injected so tests don't hit the network; nil means
// fall back to the production clone in internal/repo.
type GitHubCloner func(owner, name, branch, token string) (string, error)

type Resolver struct {
	Store        db.Store
	Runner       sshclient.Runner
	GitHubCloner GitHubCloner
}

func (r Resolver) Prepare(ctx context.Context, repo *models.Repo, branch string) (Prepared, error) {
	if repo == nil {
		return Prepared{}, fmt.Errorf("repo is required")
	}
	switch repo.SourceType {
	case "", models.SourceTypeLocal:
		sha, dirty := localGitState(repo.SourcePath)
		return Prepared{
			Path:       repo.SourcePath,
			SourceType: models.SourceTypeLocal,
			SourcePath: repo.SourcePath,
			CommitSHA:  sha,
			DirtyState: dirty,
			Cleanup:    func() {},
		}, nil
	case models.SourceTypeSSH:
		return r.prepareSSH(ctx, repo, branch)
	case models.SourceTypeGitHub:
		return r.prepareGitHub(ctx, repo, branch)
	default:
		return Prepared{}, fmt.Errorf("unsupported repo source_type %q", repo.SourceType)
	}
}

func (r Resolver) prepareSSH(ctx context.Context, repo *models.Repo, branch string) (Prepared, error) {
	if repo.RemoteNodeID == nil || *repo.RemoteNodeID == "" {
		return Prepared{}, fmt.Errorf("ssh repo has no remote_node_id")
	}
	node, err := r.Store.GetRemoteNodeByID(ctx, *repo.RemoteNodeID)
	if err != nil {
		return Prepared{}, fmt.Errorf("load remote node: %w", err)
	}
	tarData, info, err := (remote.Service{Store: r.Store, Runner: r.Runner}).Archive(ctx, node, repo.SourcePath, branch)
	if err != nil {
		return Prepared{}, err
	}
	if info.DirtyState == "dirty" {
		policy := "fail"
		if configured, cfgErr := r.Store.GetSetting(ctx, "remote_scan_dirty_policy"); cfgErr == nil && configured != "" {
			policy = configured
		}
		if policy != "allow" {
			return Prepared{}, fmt.Errorf("remote repo has uncommitted changes; commit or stash them, or set remote_scan_dirty_policy=allow")
		}
	}
	dir, err := os.MkdirTemp("", "wolf-ssh-scan-*")
	if err != nil {
		return Prepared{}, fmt.Errorf("create remote scan workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := extractTar(bytes.NewReader(tarData), dir); err != nil {
		cleanup()
		return Prepared{}, err
	}
	return Prepared{
		Path:              dir,
		SourceType:        models.SourceTypeSSH,
		RemoteNodeID:      repo.RemoteNodeID,
		SourcePath:        repo.SourcePath,
		CommitSHA:         info.CommitSHA,
		DirtyState:        info.DirtyState,
		PreparedWorkspace: dir,
		Cleanup:           cleanup,
	}, nil
}

func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	cleanDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
			return fmt.Errorf("archive contains unsafe path %q", hdr.Name)
		}
		target := filepath.Join(cleanDest, name)
		if !strings.HasPrefix(target, cleanDest+string(filepath.Separator)) && target != cleanDest {
			return fmt.Errorf("archive path escapes workspace: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777) // #nosec G115 -- masked with &0o777, so the value is bounded to valid permission bits; conversion is safe
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			continue
		default:
			continue
		}
	}
}

func localGitState(repoPath string) (string, string) {
	sha := strings.TrimSpace(runGit(repoPath, "rev-parse", "HEAD"))
	dirty := "unknown"
	if out := runGit(repoPath, "status", "--porcelain"); out != "" {
		dirty = "dirty"
	} else if sha != "" {
		dirty = "clean"
	}
	return sha, dirty
}

func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
