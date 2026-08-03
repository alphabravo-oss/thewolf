package scannerreleasebackend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

// SourceMaterializer recreates the exact source tree in a fresh managed
// workspace. Evidence rehydration alone is insufficient for Buildx and real
// quality adapters, which consume repository files.
type SourceMaterializer interface {
	Materialize(context.Context, scannerreleaseworkspace.ExecutionContext, scannerreleaseworker.StepRequest) error
}

type GitAuthorizationProvider interface {
	Authorization(context.Context, string) (string, error)
}

type GitAuthorizationProviderFunc func(context.Context, string) (string, error)

func (f GitAuthorizationProviderFunc) Authorization(ctx context.Context, sourceURL string) (string, error) {
	return f(ctx, sourceURL)
}

type GitSourceMaterializer struct {
	Runtime       CommandRuntime
	GitPath       string
	Authorization GitAuthorizationProvider

	mu    sync.Mutex
	locks map[string]*sourceLock
}

type sourceLock struct {
	mu   sync.Mutex
	refs int
}

func (m *GitSourceMaterializer) Materialize(
	ctx context.Context,
	execution scannerreleaseworkspace.ExecutionContext,
	request scannerreleaseworker.StepRequest,
) error {
	if m == nil || m.Runtime == nil || !filepath.IsAbs(request.Workspace) ||
		!fullCommitPattern.MatchString(request.DefinitionCommit) ||
		!digestPattern.MatchString(request.LockDigest) || strings.TrimSpace(execution.SourceURL) == "" {
		return errors.New("managed Git source materializer configuration or binding is invalid")
	}
	release := m.lock(request.Workspace)
	defer release()
	if err := m.verify(ctx, request); err == nil {
		return nil
	}
	git := m.GitPath
	if git == "" {
		git = "/usr/bin/git"
	}
	if !filepath.IsAbs(git) {
		return errors.New("managed Git executable must be absolute")
	}
	if err := cleanupPartialMaterialization(request.Workspace); err != nil {
		return err
	}
	stateRoot := filepath.Join(request.Workspace, ".wolf-source-materialization")
	staging := filepath.Join(stateRoot, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	authorization := ""
	if m.Authorization != nil {
		value, err := m.Authorization.Authorization(ctx, execution.SourceURL)
		if err != nil {
			return errors.New("resolve managed Git authorization")
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("managed Git authorization is invalid")
		}
		authorization = value
	}
	if authorization != "" {
		parsed, err := url.Parse(execution.SourceURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("managed Git authorization is allowed only for credential-free HTTPS sources")
		}
	}
	environment := []string{
		"PATH=/usr/bin:/bin", "HOME=/tmp", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=", "GIT_CONFIG_KEY_1=core.hooksPath",
		"GIT_CONFIG_VALUE_1=/dev/null",
	}
	if authorization != "" {
		environment[5] = "GIT_CONFIG_COUNT=3"
		environment = append(environment,
			"GIT_CONFIG_KEY_2=http.extraHeader",
			"GIT_CONFIG_VALUE_2="+authorization,
		)
	}
	commands := []Command{
		{Path: git, Args: []string{"init", "--quiet", staging}, Environment: environment},
		{Path: git, Args: []string{"-C", staging, "remote", "add", "origin", execution.SourceURL}, Environment: environment},
		{
			Path: git, Args: []string{
				"-C", staging, "fetch", "--quiet", "--no-tags", "--depth=1",
				"origin", request.DefinitionCommit,
			}, Environment: environment,
		},
		{Path: git, Args: []string{"-C", staging, "checkout", "--quiet", "--detach", "--force", "FETCH_HEAD", "--"}, Environment: environment},
	}
	for _, command := range commands {
		if _, err := m.Runtime.Run(ctx, command); err != nil {
			return fmt.Errorf("materialize exact managed Git source: %w", err)
		}
	}
	if err := rejectSourceSymlinks(staging); err != nil {
		return err
	}
	stagedRequest := request
	stagedRequest.Workspace = staging
	if err := m.verify(ctx, stagedRequest); err != nil {
		return err
	}
	if err := promoteMaterializedSource(request.Workspace, staging); err != nil {
		return err
	}
	return m.verify(ctx, request)
}

func (m *GitSourceMaterializer) verify(ctx context.Context, request scannerreleaseworker.StepRequest) error {
	git := m.GitPath
	if git == "" {
		git = "/usr/bin/git"
	}
	output, err := m.Runtime.Run(ctx, Command{
		Path: git, Args: []string{"-C", request.Workspace, "rev-parse", "HEAD"},
		Environment: []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
	})
	if err != nil || strings.TrimSpace(string(output.Stdout)) != request.DefinitionCommit {
		return errors.New("managed source HEAD does not match definition commit")
	}
	lock, err := scannerlock.LoadFile(filepath.Join(request.Workspace, scannerlock.DefaultLockPath))
	if err != nil {
		return fmt.Errorf("load restored scanner lock: %w", err)
	}
	if lock.LockDigest != request.LockDigest {
		return errors.New("restored scanner lock does not match candidate lock digest")
	}
	status, err := m.Runtime.Run(ctx, Command{
		Path: git, Args: []string{
			"-C", request.Workspace, "status", "--porcelain=v1", "-z",
			"--untracked-files=all", "--",
		},
		Environment: []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
	})
	if err != nil {
		return errors.New("inspect managed source worktree integrity")
	}
	for _, record := range strings.Split(string(status.Stdout), "\x00") {
		if record == "" {
			continue
		}
		if len(record) < 4 || record[:3] != "?? " || !managedControlPath(record[3:]) {
			return errors.New("managed source worktree differs from the exact definition commit")
		}
	}
	tracked, err := m.Runtime.Run(ctx, Command{
		Path: git, Args: []string{"-C", request.Workspace, "ls-files", "-z", "--"},
		Environment: []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
	})
	if err != nil {
		return errors.New("inspect managed source tracked paths")
	}
	for _, relative := range strings.Split(string(tracked.Stdout), "\x00") {
		if relative == "" {
			continue
		}
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative ||
			relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("managed source contains an unsafe tracked path")
		}
		info, err := os.Lstat(filepath.Join(request.Workspace, filepath.FromSlash(relative)))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed source contains a missing or symlinked tracked path")
		}
	}
	return nil
}

func managedControlPath(relative string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if clean != relative || clean == "." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, allowed := range []string{
		".wolf-release-evidence", ".wolf-release-backend-journal",
		".wolf-release-backend-results", ".wolf-release-buildx",
		".wolf-signing", ".wolf-source-materialization",
	} {
		if clean == allowed || strings.HasPrefix(clean, allowed+"/") {
			return true
		}
	}
	return clean == ".wolf-release-context.json"
}

func (m *GitSourceMaterializer) lock(workspace string) func() {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = make(map[string]*sourceLock)
	}
	lock := m.locks[workspace]
	if lock == nil {
		lock = &sourceLock{}
		m.locks[workspace] = lock
	}
	lock.refs++
	m.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(m.locks, workspace)
		}
		m.mu.Unlock()
	}
}

func rejectSourceSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed source contains symlink %q", path)
		}
		return nil
	})
}

func cleanupPartialMaterialization(workspace string) error {
	stateRoot := filepath.Join(workspace, ".wolf-source-materialization")
	installedPath := filepath.Join(stateRoot, "installed")
	value, err := os.ReadFile(installedPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(value) > 64<<10 {
		return errors.New("managed source installation ledger exceeds size limit")
	}
	for _, name := range strings.Split(strings.TrimSpace(string(value)), "\n") {
		if name == "" {
			continue
		}
		if !safeMaterializedTopLevel(name) {
			return errors.New("managed source installation ledger contains an unsafe path")
		}
		if err := os.RemoveAll(filepath.Join(workspace, name)); err != nil {
			return err
		}
	}
	return os.RemoveAll(stateRoot)
}

func promoteMaterializedSource(workspace, staging string) error {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	stateRoot := filepath.Join(workspace, ".wolf-source-materialization")
	installed := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !safeMaterializedTopLevel(name) {
			return fmt.Errorf("managed source top-level path %q is reserved or unsafe", name)
		}
		destination := filepath.Join(workspace, name)
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("managed source destination %q already exists outside the materializer ledger", name)
		} else if !os.IsNotExist(err) {
			return err
		}
		// Persist ownership before rename. A crash on either side of the rename
		// is safely recoverable by cleanupPartialMaterialization.
		installed = append(installed, name)
		if err := writeInstalledLedger(filepath.Join(stateRoot, "installed"), installed); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(staging, name), destination); err != nil {
			return err
		}
	}
	return os.Remove(staging)
}

func safeMaterializedTopLevel(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\\x00\r\n") &&
		(name == ".git" || !strings.HasPrefix(name, ".wolf-"))
}

func writeInstalledLedger(path string, installed []string) error {
	value := []byte(strings.Join(installed, "\n") + "\n")
	temporary, err := os.CreateTemp(filepath.Dir(path), ".installed-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
