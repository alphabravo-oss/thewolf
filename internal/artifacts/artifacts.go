// Package artifacts is a durable, on-disk store for scan tool output (logs
// and JSON), keyed by scan ID. It lives at ~/.wolf/artifacts/ by default.
//
// This file is a minimal restoration; the package was referenced from several
// callers (internal/api/server.go, internal/api/routes/{scans,collections,repos}.go)
// but the package implementation was not committed. The contract below preserves
// the call sites those files already make:
//
//   - artifacts.Init(rootDir)           // sets up the singleton at startup
//   - artifacts.Global                  // a *Store; nil before Init
//   - artifacts.Global.ScanDir(id)      // path of a per-scan directory
//   - artifacts.Global.DeleteScans(ids) // best-effort cleanup of scan dirs
//
// If the durable artifact persistence work resumes elsewhere, callers should
// not need to change — this package simply ensures the build link is intact.
package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store persists scan-tool output keyed by scan ID. Methods are safe for
// concurrent use.
type Store struct {
	root string
	mu   sync.Mutex
}

// Global is the process-wide artifact store. Nil until Init is called.
var Global *Store

// Init creates the directory at root if needed and installs a Store at Global.
// Subsequent calls overwrite Global with a Store pointed at the new root.
// Returns nil error if root is already a writable directory or could be created.
func Init(root string) error {
	if root == "" {
		return errors.New("artifacts: root path is empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	Global = &Store{root: root}
	return nil
}

// Root returns the configured root directory.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// ScanDir returns the path of the per-scan subdirectory for scanID. The
// directory is created lazily on first call. The returned path is always
// absolute (per filepath.Join semantics relative to s.root, which Init normalizes).
func (s *Store) ScanDir(scanID string) string {
	if s == nil || scanID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, scanID)
	_ = os.MkdirAll(dir, 0o750)
	return dir
}

// DeleteScans removes the per-scan directories for each given scan ID.
// Errors are swallowed (best-effort); the function logs nothing because the
// callers run it in a goroutine and don't observe the result.
func (s *Store) DeleteScans(scanIDs []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range scanIDs {
		if id == "" {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.root, id))
		for _, dir := range s.scanDirsForID(id) {
			_ = os.RemoveAll(dir)
		}
	}
}

// CleanupOlderThan removes artifact directories whose filesystem modtime is
// older than maxAge. It only removes on-disk artifacts; DB scan history and
// scan_artifacts rows are intentionally left intact for auditability.
func (s *Store) CleanupOlderThan(maxAge time.Duration) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	if maxAge <= 0 {
		return nil, errors.New("artifacts: maxAge must be positive")
	}
	cutoff := time.Now().Add(-maxAge)
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	return removed, nil
}

func (s *Store) scanDirsForID(scanID string) []string {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	short := scanID
	if len(short) > 8 {
		short = short[:8]
	}
	matches := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, "_"+short) || strings.HasSuffix(name, "_"+scanID) {
			matches = append(matches, filepath.Join(s.root, name))
		}
	}
	return matches
}
