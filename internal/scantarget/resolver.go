package scantarget

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	TreeDigest        string
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
		treeDigest, _ := workspaceTreeDigest(repo.SourcePath)
		return Prepared{
			Path:       repo.SourcePath,
			SourceType: models.SourceTypeLocal,
			SourcePath: repo.SourcePath,
			CommitSHA:  sha,
			TreeDigest: treeDigest,
			DirtyState: dirty,
			Cleanup:    func() {},
		}, nil
	case models.SourceTypeSSH:
		return r.prepareSSH(ctx, repo, branch)
	case models.SourceTypeGitHub:
		return r.prepareGitHub(ctx, repo, branch)
	case models.SourceTypeGit, models.SourceTypeGitLab:
		return r.prepareGit(ctx, repo, branch)
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
	if repo.SourceFingerprint != "" {
		if err := ValidateRemoteDestination(ctx, node.Host); err != nil {
			return Prepared{}, err
		}
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
	dir, err := makeScanWorkspace("wolf-ssh-scan-*")
	if err != nil {
		return Prepared{}, fmt.Errorf("create remote scan workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := extractTar(bytes.NewReader(tarData), dir); err != nil {
		cleanup()
		return Prepared{}, err
	}
	treeDigest, err := workspaceTreeDigest(dir)
	if err != nil {
		cleanup()
		return Prepared{}, fmt.Errorf("digest remote scan workspace: %w", err)
	}
	return Prepared{
		Path:              dir,
		SourceType:        models.SourceTypeSSH,
		RemoteNodeID:      repo.RemoteNodeID,
		SourcePath:        repo.SourcePath,
		CommitSHA:         info.CommitSHA,
		TreeDigest:        treeDigest,
		DirtyState:        info.DirtyState,
		PreparedWorkspace: dir,
		Cleanup:           cleanup,
	}, nil
}

func workspaceTreeDigest(root string) (string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			entries = append(entries, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, path := range entries {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		_, _ = hash.Write([]byte{0})
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(hash, "symlink\x00"+target)
		} else {
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func makeScanWorkspace(pattern string) (string, error) {
	root := strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT"))
	if root == "" {
		return os.MkdirTemp("", pattern)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, pattern)
}

// CleanupWorkspace removes only isolated workspaces created by this package.
// Cached GitHub clones and arbitrary local paths are deliberately outside the
// accepted boundary.
func CleanupWorkspace(workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	name := filepath.Base(absoluteWorkspace)
	if !strings.HasPrefix(name, "wolf-git-scan-") && !strings.HasPrefix(name, "wolf-ssh-scan-") {
		return fmt.Errorf("refusing to clean unrecognized temporary workspace")
	}
	if configuredRoot := strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT")); configuredRoot != "" {
		absoluteRoot, rootErr := filepath.Abs(configuredRoot)
		if rootErr != nil {
			return rootErr
		}
		relative, relErr := filepath.Rel(absoluteRoot, absoluteWorkspace)
		if relErr != nil || relative == "." || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("refusing to clean workspace outside WOLF_WORKSPACE_ROOT")
		}
		return os.RemoveAll(absoluteWorkspace)
	}
	if filepath.Dir(absoluteWorkspace) != filepath.Clean(os.TempDir()) {
		return fmt.Errorf("refusing to clean workspace outside the system temporary directory")
	}
	return os.RemoveAll(absoluteWorkspace)
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
