package scantarget

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
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

// PrepareExisting uses an already-materialised working tree (a fixer
// workspace) so a child scan does not clone a remote wolf-fix ref that
// has not been pushed yet. Cleanup releases any managed-workspace hold without
// deleting the tree; the original source/remediation owns deletion.
func PrepareExisting(ctx context.Context, path string, sourceType models.SourceType) (Prepared, error) {
	if path == "" {
		return Prepared{}, fmt.Errorf("existing workspace path is required")
	}
	release, err := RetainWorkspace(path)
	if err != nil {
		return Prepared{}, fmt.Errorf("retain existing workspace: %w", err)
	}
	prepared := false
	defer func() {
		if !prepared {
			release()
		}
	}()
	st, err := os.Stat(path)
	if err != nil {
		return Prepared{}, fmt.Errorf("existing workspace: %w", err)
	}
	if !st.IsDir() {
		return Prepared{}, fmt.Errorf("existing workspace is not a directory: %s", path)
	}
	sha, dirty, err := localGitState(ctx, path)
	if err != nil {
		return Prepared{}, fmt.Errorf("inspect existing workspace: %w", err)
	}
	treeDigest, err := workspaceTreeDigest(path)
	if err != nil {
		return Prepared{}, fmt.Errorf("digest existing workspace: %w", err)
	}
	if sourceType == "" {
		sourceType = models.SourceTypeLocal
	}
	prepared = true
	return Prepared{
		Path:              path,
		SourceType:        sourceType,
		SourcePath:        path,
		CommitSHA:         sha,
		TreeDigest:        treeDigest,
		DirtyState:        dirty,
		PreparedWorkspace: path,
		Cleanup:           release,
	}, nil
}

func (r Resolver) Prepare(ctx context.Context, repo *models.Repo, branch string) (Prepared, error) {
	if repo == nil {
		return Prepared{}, fmt.Errorf("repo is required")
	}
	switch repo.SourceType {
	case "", models.SourceTypeLocal:
		prepared, err := PrepareExisting(ctx, repo.SourcePath, models.SourceTypeLocal)
		// Local source ownership remains with the operator, even if it lives
		// under the managed workspace root. Hold it without scheduling deletion.
		prepared.PreparedWorkspace = ""
		return prepared, err
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

// Sync refreshes a repo's local cache (or recorded remote HEAD) without
// starting a scan. GitHub clones or pulls the workspace cache. Generic Git
// sources do a shallow fetch. Local paths re-read HEAD. SSH only probes
// remote git info — it does not archive the tree.
func (r Resolver) Sync(ctx context.Context, repo *models.Repo, branch string) (Prepared, error) {
	if repo == nil {
		return Prepared{}, fmt.Errorf("repo is required")
	}
	if repo.SourceType == models.SourceTypeSSH {
		return r.syncSSH(ctx, repo)
	}
	return r.Prepare(ctx, repo, branch)
}

func (r Resolver) syncSSH(ctx context.Context, repo *models.Repo) (Prepared, error) {
	if repo.RemoteNodeID == nil || *repo.RemoteNodeID == "" {
		return Prepared{}, fmt.Errorf("ssh repo has no remote_node_id")
	}
	node, err := r.Store.GetRemoteNodeByID(ctx, *repo.RemoteNodeID)
	if err != nil {
		return Prepared{}, fmt.Errorf("load remote node: %w", err)
	}
	info, err := (remote.Service{Store: r.Store, Runner: r.Runner}).GitInfo(ctx, node, repo.SourcePath)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		SourceType:        models.SourceTypeSSH,
		RemoteNodeID:      repo.RemoteNodeID,
		SourcePath:        repo.SourcePath,
		CommitSHA:         info.CommitSHA,
		DirtyState:        info.DirtyState,
		PreparedWorkspace: info.Path,
		Cleanup:           func() {},
	}, nil
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
	dir, cleanup, err := makeScanWorkspace("wolf-ssh-scan-*")
	if err != nil {
		return Prepared{}, fmt.Errorf("create remote scan workspace: %w", err)
	}
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
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	return digestOpenedWorkspace(dir)
}

func digestOpenedWorkspace(dir *os.Root) (string, error) {
	var entries []string
	err := fs.WalkDir(dir.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		entries = append(entries, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, path := range entries {
		info, err := dir.Lstat(filepath.FromSlash(path))
		if err != nil {
			return "", err
		}
		// Lengths, entry type and permissions make binary contents, symlink
		// text and multiple file layouts unambiguous. v1 used NUL delimiters.
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := dir.Readlink(filepath.FromSlash(path))
			if err != nil {
				return "", err
			}
			_, _ = fmt.Fprintf(hash, "%d:%s:%d:%d:", len(path), path, info.Mode(), len(target))
			_, _ = io.WriteString(hash, target)
		case info.IsDir():
			_, _ = fmt.Fprintf(hash, "%d:%s:%d:0:", len(path), path, info.Mode())
		case info.Mode().IsRegular():
			// Reuse the metadata reader's nonblocking-open pattern: a swapped
			// FIFO must fail the descriptor check rather than wait for a writer.
			file, err := dir.OpenFile(filepath.FromSlash(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return "", err
			}
			opened, err := file.Stat()
			if err != nil {
				_ = file.Close()
				return "", err
			}
			if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				_ = file.Close()
				return "", fmt.Errorf("source entry changed during digest: %s", path)
			}
			_, _ = fmt.Fprintf(hash, "%d:%s:%d:%d:", len(path), path, opened.Mode(), opened.Size())
			_, copyErr := io.CopyN(hash, file, opened.Size())
			after, statErr := file.Stat()
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if statErr != nil {
				return "", statErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			if after.Size() != opened.Size() || after.Mode() != opened.Mode() || !after.ModTime().Equal(opened.ModTime()) {
				return "", fmt.Errorf("source file changed during digest: %s", path)
			}
		default:
			return "", fmt.Errorf("unsupported source entry type: %s", path)
		}
	}
	return "sha256-v2:" + hex.EncodeToString(hash.Sum(nil)), nil
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
		case tar.TypeReg:
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

func localGitState(ctx context.Context, repoPath string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	run := func(args ...string) (string, error) {
		cmd := gitpkg.Command(ctx, nil, args...)
		cmd.Dir = repoPath
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return string(out), err
	}
	if _, err := run("rev-parse", "--is-inside-work-tree"); err != nil {
		if ctx.Err() != nil {
			return "", "unknown", ctx.Err()
		}
		// Non-Git local source remains supported. A present but broken .git
		// must not be disguised as an ordinary directory or clean checkout.
		if _, statErr := os.Lstat(filepath.Join(repoPath, ".git")); errors.Is(statErr, os.ErrNotExist) {
			return "", "unknown", nil
		}
		return "", "unknown", fmt.Errorf("inspect Git working tree: %w", err)
	}
	sha, shaErr := run("rev-parse", "--verify", "HEAD")
	if shaErr != nil {
		// An unborn branch has no commit yet, but status still has to succeed.
		if _, err := run("symbolic-ref", "--quiet", "HEAD"); err != nil {
			return "", "unknown", fmt.Errorf("inspect Git HEAD: %w", shaErr)
		}
		sha = ""
	}
	status, err := run("status", "--porcelain")
	if err != nil {
		return "", "unknown", fmt.Errorf("inspect Git status: %w", err)
	}
	dirty := "clean"
	if status != "" {
		dirty = "dirty"
	}
	return strings.TrimSpace(sha), dirty, nil
}
