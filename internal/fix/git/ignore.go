package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// IgnoredPaths uses native Git semantics, including tracked-file exclusion,
// nested rules, info/exclude, negation and anchoring. Machine/global excludes
// and inherited Git redirection do not determine Wolf's visibility policy.
// An explicit source without .git has no Git-derived decisions. Invalid finding
// paths remain visible; failed evaluation returns no partial decisions.
func IgnoredPaths(ctx context.Context, repoPath string, paths []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repoPath == "" || len(paths) == 0 {
		return nil, nil
	}
	safe := make([]string, 0, len(paths))
	requested := make(map[string]bool, len(paths))
	for _, p := range paths {
		normalized := strings.TrimPrefix(p, "./")
		if normalized == "." || !fs.ValidPath(normalized) || strings.ContainsRune(p, 0) {
			continue
		}
		if !requested[p] {
			safe = append(safe, p)
			requested[p] = true
		}
	}
	if len(safe) == 0 {
		return nil, nil
	}
	// ponytail: Git evaluation is capped at ten seconds per batch; qualify large
	// repositories before replacing this bound with persisted policy snapshots.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Do not discover an unrelated repository above the registered source root.
	if _, err := root.Lstat(".git"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	gitdir, err := openWorktreeGitDir(ctx, root, os.OpenRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gitdir.Close() }()
	common, err := openCommonGitDir(ctx, gitdir, os.OpenRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = common.Close() }()
	// Git silently skips some unreadable/non-regular policies. Validate every
	// candidate's rule ancestry and the common exclude file before trusting it.
	if err := validateIgnoreFile(ctx, common, "info/exclude"); err != nil {
		return nil, err
	}
	checked := map[string]bool{}
	for _, p := range safe {
		for dir := path.Dir(strings.TrimPrefix(p, "./")); ; dir = path.Dir(dir) {
			name := path.Join(dir, ".gitignore")
			if !checked[name] {
				if err := validateIgnoreFile(ctx, root, filepath.FromSlash(name)); err != nil {
					return nil, err
				}
				checked[name] = true
			}
			if dir == "." {
				break
			}
		}
	}
	cmd := Command(ctx, nil, "-C", repoPath, "--git-dir=.git", "--work-tree=.", "-c", "core.excludesFile="+os.DevNull, "check-ignore", "--stdin", "-z")
	cmd.Stdin = strings.NewReader(strings.Join(safe, "\x00") + "\x00")
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("evaluate Git ignores: %w", ctx.Err())
	}
	// Git can report unreadable ignore files as warnings while exiting zero or
	// one. Such a result is incomplete, even when some paths matched.
	if diagnostics.Len() != 0 {
		return nil, errors.New("Git ignore evaluation reported diagnostics; results are incomplete")
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(out) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("evaluate Git ignores: %w", err)
	}
	if len(out) == 0 || out[len(out)-1] != 0 {
		return nil, errors.New("Git ignore response is not NUL-terminated")
	}
	ignored := make(map[string]bool)
	for _, p := range strings.Split(string(out[:len(out)-1]), "\x00") {
		if !requested[p] {
			return nil, errors.New("Git ignore response contains an unrequested path")
		}
		ignored[p] = true
	}
	return ignored, nil
}

// validateIgnoreFile checks the opened file as well as its directory entry.
// No symlink policy is followed; callers can place ordinary rules at that path.
func validateIgnoreFile(ctx context.Context, root *os.Root, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Git ignore policy %s must be a regular file", name)
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Git ignore policy %s must be a regular file", name)
	}
	return nil
}
