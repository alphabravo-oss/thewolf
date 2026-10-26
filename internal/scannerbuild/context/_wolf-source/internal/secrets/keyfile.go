package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOrCreateKeyFile reads an existing key, or atomically publishes a generated
// key if absent. Callers validate their own encoding. Existing entries are never
// replaced, including unreadable, malformed, or concurrently created keys.
func LoadOrCreateKeyFile(path string, generate func() ([]byte, error)) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open key directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(path)
	data, err := root.ReadFile(name)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	data, err = generate()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	tempName := "." + name + "-" + rand.Text()
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create temporary key: %w", err)
	}
	defer func() { _ = root.Remove(tempName) }()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close key: %w", err)
	}
	// Link publishes the complete file only if the final name is absent.
	// Rename would overwrite another process's winning key.
	if err := root.Link(tempName, name); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("publish key: %w", err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open key directory for sync: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return nil, fmt.Errorf("sync key directory: %w", err)
	}
	// Read the canonical name even when we lost the race. Never return a
	// generated key that did not become the persisted key.
	data, err = root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read published key: %w", err)
	}
	return data, nil
}
