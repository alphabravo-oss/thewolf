// Package fixstore is the durable, on-disk home for a fix job's two reviewable
// artifacts: the streamed worker log and the proposed unified diff. It is the
// seam between the out-of-process `wolf fixer` worker (which WRITES) and the
// wolf server (which RELAYS the log over SSE and serves the diff). Both sides
// agree on a single layout under the artifacts root so neither needs a shared
// in-process broker:
//
//	<root>/fixes/<jobID>.log           — append-only progress log (the worker's Logf)
//	<root>/fixes/<jobID>.diff          — the assembled branch diff (the DiffStore sink)
//	<root>/fixes/console-<id>.log      — fixer console transcript (login / shell)
//
// The server tails the .log to feed its SSE relay and reads the .diff for
// GET /fixes/{id}/diff. Keeping this tiny and filesystem-only means tests use a
// real temp dir (cheap) and nothing reaches for docker or the network.
package fixstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// dirName is the subdirectory under the artifacts root that holds fix artifacts.
const dirName = "fixes"

// Store reads and writes a fix job's log and diff artifacts beneath an
// artifacts root. Methods are safe for concurrent use.
type Store struct {
	root string
	mu   sync.Mutex
}

// New returns a Store rooted at the given artifacts directory. The fixes
// subdirectory is created lazily on first write.
func New(root string) *Store {
	return &Store{root: root}
}

// Root is the artifacts directory this store writes under.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// fixesDir is the directory holding per-job artifacts, created on demand.
func (s *Store) fixesDir() (string, error) {
	dir := filepath.Join(s.root, dirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// LogPath returns the on-disk path of a job's log artifact (it may not exist).
func (s *Store) LogPath(jobID string) string {
	return filepath.Join(s.root, dirName, jobID+".log")
}

// DiffPath returns the on-disk path of a job's diff artifact (it may not exist).
func (s *Store) DiffPath(jobID string) string {
	return filepath.Join(s.root, dirName, jobID+".diff")
}

// ConsoleLogPath is the transcript for a fixer console session.
func (s *Store) ConsoleLogPath(consoleID string) string {
	return filepath.Join(s.root, dirName, "console-"+consoleID+".log")
}

// AppendLog writes one progress line (a trailing newline is added). It is the
// worker's Logf sink; the server tails the same file for the SSE relay.
func (s *Store) AppendLog(jobID, line string) error {
	dir, err := s.fixesDir()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(dir, jobID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640) // #nosec G304 -- jobID is a server-issued UUID under the artifacts root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, line)
	return err
}

// ReadLog returns the full log artifact for a job, or empty if none exists yet.
func (s *Store) ReadLog(jobID string) (string, error) {
	data, err := os.ReadFile(s.LogPath(jobID)) // #nosec G304 -- jobID is a server-issued UUID under the artifacts root
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveDiff persists the assembled branch diff for a job and returns its
// artifact ID (the on-disk filename). It satisfies the orchestrator's DiffStore
// interface so the worker can wire it directly.
func (s *Store) SaveDiff(ctx context.Context, jobID, diff string) (string, error) {
	dir, err := s.fixesDir()
	if err != nil {
		return "", err
	}
	name := jobID + ".diff"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(diff), 0o640); err != nil { // #nosec G304 -- jobID is a server-issued UUID under the artifacts root
		return "", err
	}
	return name, nil
}

// AppendConsole writes one transcript line for a fixer console session.
func (s *Store) AppendConsole(consoleID, line string) error {
	return s.AppendLog("console-"+consoleID, line)
}

// AppendConsoleRaw writes PTY bytes as-is so a browser terminal can replay
// CSI sequences and carriage returns. Do not add a trailing newline.
func (s *Store) AppendConsoleRaw(consoleID string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	dir, err := s.fixesDir()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(dir, "console-"+consoleID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640) // #nosec G304 -- consoleID is a server-issued UUID
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	return err
}

// ReadConsole returns the console transcript, or empty if none exists yet.
func (s *Store) ReadConsole(consoleID string) (string, error) {
	return s.ReadLog("console-" + consoleID)
}

// ReadDiff returns the proposed diff for a job, or empty if none exists yet.
func (s *Store) ReadDiff(jobID string) (string, error) {
	data, err := os.ReadFile(s.DiffPath(jobID)) // #nosec G304 -- jobID is a server-issued UUID under the artifacts root
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
