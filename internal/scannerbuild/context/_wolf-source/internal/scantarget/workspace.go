package scantarget

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var ErrWorkspaceInUse = errors.New("workspace is still in use")

func workspaceRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT"))
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Abs(root)
}

func managedWorkspaceName(name string) bool {
	return filepath.Base(name) == name && (strings.HasPrefix(name, "wolf-git-scan-") || strings.HasPrefix(name, "wolf-ssh-scan-"))
}

func workspaceLeaseDir() string {
	if strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT")) == "" {
		// The system temporary directory may be shared by unrelated users.
		return ".wolf-workspace-locks-" + strconv.Itoa(os.Getuid())
	}
	return ".wolf-workspace-locks"
}

// The stable guard serializes lease creation/removal, preventing two lock inodes
// for one name. Recursive deletion never holds this root-wide guard.
func workspaceGuard(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(workspaceLeaseDir()+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockWorkspaceFile(file, true, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func lockWorkspace(root *os.Root, name string, exclusive bool) (*os.File, error) {
	guard, err := workspaceGuard(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = guard.Close() }()
	return lockWorkspaceGuarded(root, name, exclusive)
}

// Caller holds the namespace guard. Writable regular files support locks on
// NFS and Windows without polluting the scanned source or depending on its files.
func lockWorkspaceGuarded(root *os.Root, name string, exclusive bool) (*os.File, error) {
	dir, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat(".")
	current, currentErr := root.Lstat(name)
	if !exclusive {
		current, currentErr = root.Stat(name)
	}
	if err != nil || currentErr != nil || !os.SameFile(info, current) {
		return nil, errors.New("workspace changed while acquiring its lock")
	}
	if err := root.MkdirAll(workspaceLeaseDir(), 0700); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(filepath.Join(workspaceLeaseDir(), name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockWorkspaceFile(file, exclusive, true); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// Remove a crash-orphaned lease only while its source is absent and no holder
// remains. Close before unlink for Windows, with the guard excluding new opens.
func pruneWorkspaceLease(root *os.Root, name string) error {
	guard, err := workspaceGuard(root)
	if err != nil {
		return err
	}
	defer func() { _ = guard.Close() }()
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lease := filepath.Join(workspaceLeaseDir(), name)
	file, err := root.OpenFile(lease, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = lockWorkspaceFile(file, true, true)
	closeErr := file.Close()
	if errors.Is(err, ErrWorkspaceInUse) {
		return nil
	}
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return root.Remove(lease)
}

// RetainWorkspace protects a managed source until the returned release is called.
// Sources outside the reserved managed root require no hold and are never reaped.
func RetainWorkspace(path string) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	parent, err := workspaceRoot()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(parent)
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	files := map[string]*os.File{}
	release := sync.OnceFunc(func() {
		for _, file := range files {
			_ = file.Close()
		}
	})
	// Protect both lexical and resolved ancestry: a subtree or symlink may
	// depend on a managed directory even when its own basename is unrelated.
	for _, source := range []string{absolute, resolved} {
		for candidate := source; candidate != filepath.Dir(candidate); candidate = filepath.Dir(candidate) {
			name := filepath.Base(candidate)
			if !managedWorkspaceName(name) || files[name] != nil {
				continue
			}
			info, err := os.Stat(filepath.Dir(candidate))
			if err != nil {
				release()
				return nil, err
			}
			if !os.SameFile(info, rootInfo) {
				continue
			}
			file, err := lockWorkspace(root, name, false)
			if err != nil {
				release()
				return nil, err
			}
			files[name] = file
		}
	}
	return release, nil
}

func makeScanWorkspace(pattern string) (string, func(), error) {
	parent, err := workspaceRoot()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(parent, 0750); err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", nil, err
	}
	name := strings.TrimSuffix(pattern, "*") + rand.Text()
	if !managedWorkspaceName(name) {
		_ = root.Close()
		return "", nil, errors.New("invalid managed workspace pattern")
	}
	guard, err := workspaceGuard(root)
	if err != nil {
		_ = root.Close()
		return "", nil, err
	}
	if err := root.Mkdir(name, 0700); err != nil {
		_ = guard.Close()
		_ = root.Close()
		return "", nil, err
	}
	file, err := lockWorkspaceGuarded(root, name, false)
	if err != nil {
		err = errors.Join(err, root.Remove(name))
		_ = guard.Close()
		err = errors.Join(err, pruneWorkspaceLease(root, name))
		_ = root.Close()
		return "", nil, err
	}
	_ = guard.Close()
	cleanup := sync.OnceFunc(func() {
		_ = file.Close()
		if _, err := cleanupOpenedWorkspace(root, name, time.Time{}); err != nil {
			wolflog.Warn().Err(err).Str("workspace", name).Msg("workspace retained after cleanup failure")
		}
		_ = root.Close()
	})
	return filepath.Join(parent, name), cleanup, nil
}

// CleanupWorkspace removes only a direct managed child of the configured root
// (or system temporary directory), and only when no cooperating process holds it.
func CleanupWorkspace(workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	parent, err := workspaceRoot()
	if err != nil {
		return err
	}
	if filepath.Dir(absolute) != parent || !managedWorkspaceName(filepath.Base(absolute)) {
		return errors.New("refusing to clean workspace outside its managed root")
	}
	root, err := os.OpenRoot(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	_, err = cleanupOpenedWorkspace(root, filepath.Base(absolute), time.Time{})
	return err
}

func cleanupOpenedWorkspace(root *os.Root, name string, before time.Time) (bool, error) {
	file, err := lockWorkspace(root, name, true)
	if errors.Is(err, os.ErrNotExist) {
		return false, pruneWorkspaceLease(root, name)
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	info, err := root.Lstat(name)
	if err != nil {
		return false, err
	}
	if !before.IsZero() && !info.ModTime().Before(before) {
		return false, nil
	}
	if err := root.RemoveAll(name); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return true, err
	}
	return true, pruneWorkspaceLease(root, name)
}

// ReapWorkspaces retains busy directories and returns partial progress with any
// real cleanup errors. Metadata checks and deletion share the opened root.
func ReapWorkspaces(path string, maxAge time.Duration) (removed, retained int, err error) {
	if maxAge <= 0 {
		return 0, 0, errors.New("workspace retention age must be positive")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, 0, err
	}
	before := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() || !managedWorkspaceName(entry.Name()) {
			continue
		}
		deleted, cleanupErr := cleanupOpenedWorkspace(root, entry.Name(), before)
		if deleted {
			removed++
		}
		if errors.Is(cleanupErr, ErrWorkspaceInUse) {
			retained++
		} else if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup %s: %w", entry.Name(), cleanupErr))
		}
	}
	leases, readErr := fs.ReadDir(root.FS(), workspaceLeaseDir())
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		err = errors.Join(err, readErr)
	}
	for _, lease := range leases {
		if managedWorkspaceName(lease.Name()) {
			if pruneErr := pruneWorkspaceLease(root, lease.Name()); pruneErr != nil {
				err = errors.Join(err, fmt.Errorf("prune lease %s: %w", lease.Name(), pruneErr))
			}
		}
	}
	return removed, retained, err
}
