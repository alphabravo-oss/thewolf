package git

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

const maxRefMetadataBytes = 8 << 20
const maxRefEntries = 10000

// BranchesRoot reads only reference metadata through pinned roots. It never
// runs Git, reads configuration, or consults process-global Git environment.
// openExternal must enforce the caller's policy for worktree/submodule gitdirs
// and common directories outside the working tree. Nil rejects those pointers.
func BranchesRoot(ctx context.Context, worktree *os.Root, openExternal func(string) (*os.Root, error)) ([]string, string, error) {
	gitdir, err := openWorktreeGitDir(ctx, worktree, openExternal)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = gitdir.Close() }()
	common, err := openCommonGitDir(ctx, gitdir, openExternal)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = common.Close() }()
	objects, err := common.Stat("objects")
	if err != nil {
		return nil, "", err
	}
	if !objects.IsDir() {
		return nil, "", errors.New("Git object storage is not a directory")
	}
	if _, err := common.Lstat("reftable"); err == nil {
		return nil, "", errors.New("reftable branch metadata is not supported")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}

	// Snapshot only bounded reference files into the installed Git parser.
	// Configuration, includes, objects, alternates and hooks are never copied.
	snapshot := memfs.New()
	remainingBytes, remainingEntries := maxRefMetadataBytes, maxRefEntries
	copyFile := func(root *os.Root, name, destination string, limit int) error {
		value, err := readGitMetadata(ctx, root, name, min(limit, remainingBytes))
		if err != nil {
			return err
		}
		remainingBytes -= len(value)
		if destination == "packed-refs" {
			value, err = packedBranchRefs(value)
		} else {
			err = validateRefValue(value)
		}
		if err != nil {
			return err
		}
		return util.WriteFile(snapshot, destination, value, 0600)
	}
	if err := copyFile(gitdir, "HEAD", "HEAD", 4096); err != nil {
		return nil, "", err
	}
	if err := copyFile(common, "packed-refs", "packed-refs", maxRefMetadataBytes); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	var copyRefs func(*os.Root, string, int) error
	copyRefs = func(root *os.Root, prefix string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return errors.New("Git reference metadata exceeds the directory/entry limit")
		}
		dir, err := root.Open(".")
		if err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(remainingEntries + 1)
		_ = dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		remainingEntries -= len(entries)
		if remainingEntries < 0 {
			return errors.New("Git reference metadata exceeds the entry limit")
		}
		for _, entry := range entries {
			name := filepath.Join(prefix, entry.Name())
			if entry.IsDir() {
				child, err := root.OpenRoot(entry.Name())
				if err != nil {
					return err
				}
				err = copyRefs(child, name, depth+1)
				_ = child.Close()
				if err != nil {
					return err
				}
			} else if err := copyFile(root, entry.Name(), name, 4096); err != nil {
				return err
			}
		}
		return nil
	}
	for _, path := range []string{"refs/heads", "refs/remotes"} {
		root, err := common.OpenRoot(filepath.FromSlash(path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		err = copyRefs(root, path, 0)
		_ = root.Close()
		if err != nil {
			return nil, "", err
		}
	}
	refs, err := dotgit.New(snapshot).Refs()
	if err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(refs) > maxRefEntries {
		return nil, "", errors.New("Git reference metadata exceeds the reference limit")
	}
	current := "HEAD"
	seen := make(map[string]bool)
	var branches []string
	for _, ref := range refs {
		name := ref.Name()
		if err := name.Validate(); err != nil {
			return nil, "", fmt.Errorf("invalid Git reference: %w", err)
		}
		if name == plumbing.HEAD {
			if ref.Type() == plumbing.SymbolicReference {
				if err := ref.Target().Validate(); err != nil {
					return nil, "", err
				}
				current = ref.Target().Short()
			}
			continue
		}
		branch := ""
		if name.IsBranch() {
			branch = strings.TrimPrefix(name.String(), "refs/heads/")
		} else if name.IsRemote() && !strings.HasSuffix(name.String(), "/HEAD") {
			_, branch, _ = strings.Cut(strings.TrimPrefix(name.String(), "refs/remotes/"), "/")
		}
		if branch != "" && !seen[branch] {
			branches = append(branches, branch)
			seen[branch] = true
		}
	}
	sort.Strings(branches)
	return branches, current, nil
}

func openMetadataDirectory(root *os.Root, pointer string, openExternal func(string) (*os.Root, error)) (*os.Root, error) {
	if pointer == "" || strings.ContainsAny(pointer, "\x00\r\n") {
		return nil, errors.New("invalid Git metadata directory pointer")
	}
	if filepath.IsLocal(pointer) {
		return root.OpenRoot(pointer)
	}
	if openExternal == nil {
		return nil, errors.New("Git metadata directory is outside the permitted root")
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(root.Name(), pointer)
	}
	return openExternal(pointer)
}

func readGitMetadata(ctx context.Context, root *os.Root, name string, maximum int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// O_NONBLOCK prevents a substituted FIFO from hanging before fstat. Only
	// regular files are read, including when an entry changed during lookup.
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(maximum) {
		return nil, errors.New("Git reference metadata is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximum {
		return nil, errors.New("Git reference metadata exceeds the byte limit")
	}
	return data, ctx.Err()
}

// The Git library accepts malformed hexadecimal hashes as partial/zero values;
// reject those before parsing so corrupt metadata cannot look like a valid repo.
func validRefHash(value string) bool {
	if (len(value) != 40 && len(value) != 64) || strings.Trim(value, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateRefValue(data []byte) error {
	value := strings.TrimSpace(string(data))
	if target, ok := strings.CutPrefix(value, "ref: "); ok {
		return plumbing.ReferenceName(target).Validate()
	}
	if !validRefHash(value) {
		return errors.New("invalid Git reference hash")
	}
	return nil
}

// Tags and other namespaces are irrelevant to branch selection. Keep them out
// of the snapshot so a repository with many release tags need not enumerate
// every tag as an in-memory Git reference to show a handful of branches.
func packedBranchRefs(data []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var selected bytes.Buffer
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value, name, _ := strings.Cut(strings.TrimPrefix(line, "^"), " ")
		if !validRefHash(value) {
			return nil, errors.New("invalid packed Git reference hash")
		}
		if strings.HasPrefix(line, "^") {
			continue
		}
		ref := plumbing.ReferenceName(name)
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if !ref.IsBranch() && !ref.IsRemote() {
			continue
		}
		count++
		if count > maxRefEntries {
			return nil, errors.New("packed Git references exceed the entry limit")
		}
		selected.WriteString(line)
		selected.WriteByte('\n')
	}
	return selected.Bytes(), scanner.Err()
}

func openWorktreeGitDir(ctx context.Context, worktree *os.Root, openExternal func(string) (*os.Root, error)) (*os.Root, error) {
	gitdir, err := worktree.OpenRoot(".git")
	if err != nil {
		data, readErr := readGitMetadata(ctx, worktree, ".git", 4096)
		if readErr != nil {
			return nil, readErr
		}
		pointer, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if !ok {
			return nil, errors.New("invalid .git directory pointer")
		}
		gitdir, err = openMetadataDirectory(worktree, pointer, openExternal)
		if err != nil {
			return nil, err
		}
	}
	return gitdir, nil
}

func openCommonGitDir(ctx context.Context, gitdir *os.Root, openExternal func(string) (*os.Root, error)) (*os.Root, error) {
	data, err := readGitMetadata(ctx, gitdir, "commondir", 4096)
	if errors.Is(err, os.ErrNotExist) {
		return gitdir.OpenRoot(".")
	}
	if err != nil {
		return nil, err
	}
	return openMetadataDirectory(gitdir, strings.TrimSpace(string(data)), openExternal)
}
