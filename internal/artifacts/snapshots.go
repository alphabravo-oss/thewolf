package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

var ErrIntegrity = errors.New("artifact content does not match its recorded checksum")
var ErrScanScope = errors.New("artifact is not bound to its scan")

func validComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00") && filepath.Base(name) == name
}

// openScanRoot rejects directory aliases and pins the directory checked by
// Lstat. Opening through the parent root alone would permit sibling symlinks.
func openScanRoot(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrScanScope
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, ErrScanScope
	}
	return root, nil
}

// Open pins a regular file beneath the configured artifact root. Nonblocking
// open prevents a substituted FIFO from hanging before its type is checked.
func Open(path string) (*os.File, error) {
	if Global == nil {
		return nil, errors.New("artifacts: backend is not initialized")
	}
	key, err := Global.Key(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(Global.Root())
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return openRegular(root, filepath.FromSlash(key))
}

func openRegular(root *os.Root, path string) (*os.File, error) {
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("artifacts: expected a regular file")
	}
	if !singleLink(file, info) {
		_ = file.Close()
		return nil, ErrScanScope
	}
	return file, nil
}

// Publish writes a private snapshot, syncs it and atomically publishes its
// directory. Each record owns a unique path; subsequent writes cannot replace
// it. Callers record the returned metadata only after successful publication.
func Publish(ctx context.Context, scanID, filename string, content io.Reader) (*models.ScanArtifact, error) {
	if Global == nil {
		return nil, errors.New("artifacts: backend is not initialized")
	}
	for _, name := range []string{scanID, filename} {
		if !validComponent(name) {
			return nil, errors.New("artifacts: scan ID and filename must be single path components")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend := Global
	parent, err := os.OpenRoot(backend.Root())
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	if err := parent.MkdirAll(scanID, 0750); err != nil {
		return nil, err
	}
	root, err := openScanRoot(parent, scanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	id := uuid.NewString()
	temporary := "." + id + ".tmp"
	destination := id
	if err := root.Mkdir(temporary, 0700); err != nil {
		return nil, err
	}
	defer func() { _ = root.RemoveAll(temporary) }()
	file, err := root.OpenFile(filepath.Join(temporary, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, digest), content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr, ctx.Err()); err != nil {
		return nil, err
	}
	if err := root.Rename(temporary, destination); err != nil {
		return nil, err
	}
	// Directory fsync is unavailable on Windows; power-loss qualification there
	// remains a platform gate. Native Unix publication syncs the complete chain.
	if runtime.GOOS != "windows" {
		for _, item := range []struct {
			root *os.Root
			path string
		}{{root, destination}, {root, "."}, {parent, "."}} {
			dir, err := item.root.Open(item.path)
			if err != nil {
				return nil, err
			}
			err = errors.Join(dir.Sync(), dir.Close())
			if err != nil {
				return nil, err
			}
		}
	}
	key := filepath.ToSlash(filepath.Join(scanID, destination, filename))
	return &models.ScanArtifact{ID: id, ScanID: scanID, FilePath: filepath.Join(backend.Root(), filepath.FromSlash(key)), StorageKey: key, FileSize: size, ChecksumSHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// ReadArtifact returns the exact bytes described by a recorded checksum. Legacy
// rows without one remain readable but cannot be asserted to be verified.
func ReadArtifact(artifact models.ScanArtifact) ([]byte, error) {
	if Global == nil {
		return nil, errors.New("artifacts: backend is not initialized")
	}
	if !validComponent(artifact.ScanID) {
		return nil, ErrScanScope
	}
	backend := Global
	path := artifact.FilePath
	if artifact.StorageKey != "" {
		path = filepath.Join(backend.Root(), filepath.FromSlash(artifact.StorageKey))
	}
	key, err := backend.Key(path)
	if err != nil || (artifact.StorageKey != "" && key != artifact.StorageKey) {
		return nil, ErrScanScope
	}
	directory, name, ok := strings.Cut(key, "/")
	if !ok || name == "" {
		return nil, ErrScanScope
	}
	legacy := directory != artifact.ScanID
	shortID := artifact.ScanID[:min(8, len(artifact.ScanID))]
	if legacy && !strings.HasSuffix(directory, "_"+shortID) && !strings.HasSuffix(directory, "_"+artifact.ScanID) {
		return nil, ErrScanScope
	}
	parent, err := os.OpenRoot(backend.Root())
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	root, err := openScanRoot(parent, directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if legacy {
		manifest, err := openRegular(root, "manifest.json")
		if err != nil {
			return nil, ErrScanScope
		}
		// ponytail: legacy identity manifests are capped at 1 MiB. Larger old
		// bundles require an explicit verified migration to full-ID storage.
		data, readErr := io.ReadAll(io.LimitReader(manifest, (1<<20)+1))
		closeErr := manifest.Close()
		var identity struct {
			ScanID string `json:"scan_id"`
		}
		if readErr != nil || closeErr != nil || len(data) > 1<<20 || json.Unmarshal(data, &identity) != nil || identity.ScanID != artifact.ScanID {
			return nil, ErrScanScope
		}
	}
	file, err := openRegular(root, filepath.FromSlash(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrScanScope, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if artifact.ChecksumSHA256 != "" {
		digest := sha256.Sum256(data)
		if int64(len(data)) != artifact.FileSize || !strings.EqualFold(hex.EncodeToString(digest[:]), artifact.ChecksumSHA256) {
			return nil, fmt.Errorf("%w: %s", ErrIntegrity, artifact.ID)
		}
	}
	return data, nil
}
