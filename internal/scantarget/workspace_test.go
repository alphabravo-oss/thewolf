package scantarget

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestManagedWorkspaceRetainedDuringPreparationAndReuse(t *testing.T) {
	for _, mode := range []string{"resumed", "local-source", "parent-alias", "source-alias", "subdirectory"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("WOLF_WORKSPACE_ROOT", root)
			path, cleanup, err := makeScanWorkspace("wolf-git-scan-*")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			old := time.Now().Add(-96 * time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			if err := CleanupWorkspace(path); !errors.Is(err, ErrWorkspaceInUse) {
				t.Fatalf("preparing workspace was not protected: %v", err)
			}
			if removed, retained, err := ReapWorkspaces(root, 72*time.Hour); err != nil || removed != 0 || retained != 1 {
				t.Fatalf("active reap = %d/%d, %v", removed, retained, err)
			}
			var prepared Prepared
			borrowPath := path
			if mode == "parent-alias" {
				alias := filepath.Join(t.TempDir(), "workspace-root")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				borrowPath = filepath.Join(alias, filepath.Base(path))
			} else if mode == "source-alias" {
				borrowPath = filepath.Join(root, "friendly-source")
				if err := os.Symlink(filepath.Base(path), borrowPath); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "subdirectory" {
				borrowPath = filepath.Join(path, "src")
				if err := os.Mkdir(borrowPath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "resumed" {
				prepared, err = (Resolver{}).Prepare(t.Context(), &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: borrowPath}, "")
				if prepared.PreparedWorkspace != "" {
					t.Fatal("local source acquired temporary ownership")
				}
			} else {
				prepared, err = PrepareExisting(t.Context(), path, models.SourceTypeGit)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(prepared.Cleanup)
			cleanup() // the prior worker/enrichment owner finishes first
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("prior owner deleted a reused workspace: %v", err)
			}
			if err := CleanupWorkspace(path); !errors.Is(err, ErrWorkspaceInUse) {
				t.Fatalf("resumed workspace was not protected: %v", err)
			}
			prepared.Cleanup() // release the final active use, without deletion
			if _, err := os.Stat(path); err != nil {
				t.Fatal("borrowed workspace release deleted the source")
			}
			if removed, retained, err := ReapWorkspaces(root, 72*time.Hour); err != nil || removed != 1 || retained != 0 {
				t.Fatalf("idle reap = %d/%d, %v", removed, retained, err)
			}
		})
	}
}

func TestWorkspaceHoldSurvivesAnotherProcessCleanup(t *testing.T) {
	if path := os.Getenv("WOLF_WORKSPACE_HOLD_CHILD"); path != "" {
		release, err := RetainWorkspace(path)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if _, err := os.Stdout.WriteString("ready\n"); err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "crash"}[crash], func(t *testing.T) {
			t.Setenv("WOLF_WORKSPACE_ROOT", t.TempDir())
			path, cleanup, err := makeScanWorkspace("wolf-ssh-scan-*")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWorkspaceHoldSurvivesAnotherProcessCleanup$")
			cmd.Env = append(os.Environ(), "WOLF_WORKSPACE_HOLD_CHILD="+path)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			reader := bufio.NewReader(output)
			if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatalf("child did not retain the workspace: %q, %v", line, err)
			}
			cleanup()
			if err := CleanupWorkspace(path); !errors.Is(err, ErrWorkspaceInUse) {
				t.Fatalf("another process's source was not protected: %v", err)
			}
			if crash {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			_ = input.Close()
			_, _ = io.Copy(io.Discard, reader)
			if err := cmd.Wait(); err != nil && !crash {
				t.Fatal(err)
			}
			if err := CleanupWorkspace(path); err != nil {
				t.Fatalf("lock was not released on process termination: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("idle source was not removed")
			}
		})
	}
}

func TestWorkspaceCleanupPinsRootAndRejectsAliases(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	t.Setenv("WOLF_WORKSPACE_ROOT", root)
	path, cleanup, err := makeScanWorkspace("wolf-git-scan-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	name := filepath.Base(path)
	sentinel := filepath.Join(outside, name)
	if err := os.Mkdir(sentinel, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "wolf-git-scan-link")
	if err := os.Symlink(sentinel, alias); err != nil {
		t.Fatal(err)
	}
	// The old lexical boundary accepted a nested path through this link and
	// RemoveAll followed its parent into the outside directory.
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{sentinel, alias, filepath.Join(root, "nested", name), root} {
		if err := CleanupWorkspace(unsafe); err == nil {
			t.Fatalf("unsafe cleanup accepted: %s", unsafe)
		}
	}
	held := root + "-held"
	t.Cleanup(func() { _ = os.RemoveAll(held) })
	if err := os.Rename(root, held); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(held, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original opened root was not cleaned")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("replacement root was changed")
	}
}

func TestWorkspaceReapRetainsRecentAndUnmanagedContent(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-96 * time.Hour)
	for _, name := range []string{"wolf-git-scan-old", "wolf-ssh-scan-fresh", "operator-source"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if name != "wolf-ssh-scan-fresh" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if removed, retained, err := ReapWorkspaces(root, 72*time.Hour); err != nil || removed != 1 || retained != 0 {
		t.Fatalf("reap = %d/%d, %v", removed, retained, err)
	}
	for _, name := range []string{"wolf-ssh-scan-fresh", "operator-source"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("retained source missing: %s", name)
		}
	}
	if _, _, err := ReapWorkspaces(root, 0); err == nil {
		t.Fatal("zero age accepted")
	}
	if _, _, err := ReapWorkspaces(filepath.Join(root, "missing"), time.Hour); err == nil {
		t.Fatal("filesystem failure reported successful cleanup")
	}
}

func TestLocalSourcePreservesLiteralPathAndOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), " checkout ")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := (Resolver{}).Prepare(t.Context(), &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: path}, "")
	if err != nil || prepared.Path != path || prepared.SourcePath != path || prepared.PreparedWorkspace != "" {
		t.Fatalf("local source identity/ownership changed: %+v, %v", prepared, err)
	}
	prepared.Cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("local source was deleted")
	}
}

func TestWorkspaceLeaseMetadataIsOutsideSourceAndPruned(t *testing.T) {
	rootPath := t.TempDir()
	t.Setenv("WOLF_WORKSPACE_ROOT", rootPath)
	path, cleanup, err := makeScanWorkspace("wolf-git-scan-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("lease polluted source: %v, %v", entries, err)
	}
	leasePath := filepath.Join(rootPath, workspaceLeaseDir(), filepath.Base(path))
	info, err := os.Stat(leasePath)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("lease is not a private regular file: %v, %v", info, err)
	}
	cleanup()
	entries, err = os.ReadDir(filepath.Join(rootPath, workspaceLeaseDir()))
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed workspace leaked lease: %v, %v", entries, err)
	}

	// Simulate interruption after source deletion but before lease removal.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	name := "wolf-git-scan-orphan"
	if err := root.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	lease, err := lockWorkspace(root, name, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err := root.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReapWorkspaces(rootPath, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(filepath.Join(workspaceLeaseDir(), name)); err != nil {
		t.Fatalf("reap unlinked a held lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReapWorkspaces(rootPath, time.Hour); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(rootPath, workspaceLeaseDir()))
	if err != nil || len(entries) != 0 {
		t.Fatalf("reap leaked orphan lease: %v, %v", entries, err)
	}
}

func TestDefaultWorkspaceMetadataUsesUserNamespace(t *testing.T) {
	t.Setenv("WOLF_WORKSPACE_ROOT", "")
	temp := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temp)
	}
	if workspaceLeaseDir() != ".wolf-workspace-locks-"+strconv.Itoa(os.Getuid()) {
		t.Fatal("default workspace metadata is shared by unrelated users")
	}
	path, cleanup, err := makeScanWorkspace("wolf-ssh-scan-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if err := CleanupWorkspace(path); !errors.Is(err, ErrWorkspaceInUse) {
		t.Fatalf("default source not protected: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default source not removed: %v", err)
	}
}
