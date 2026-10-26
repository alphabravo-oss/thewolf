package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPublishSnapshots(t *testing.T) {
	previous := Global
	t.Cleanup(func() { Global = previous })
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	const count = 16
	records := make(chan *models.ScanArtifact, count)
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			content := fmt.Sprintf("snapshot %d", i)
			record, err := Publish(t.Context(), "scan", "gate-result.json", strings.NewReader(content))
			if err != nil {
				failures <- err
				return
			}
			data, err := ReadArtifact(*record)
			if err != nil || string(data) != content {
				failures <- fmt.Errorf("read snapshot: %q, %v", data, err)
				return
			}
			records <- record
		}()
	}
	wg.Wait()
	close(records)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	paths := map[string]bool{}
	var first *models.ScanArtifact
	for record := range records {
		if paths[record.StorageKey] {
			t.Errorf("duplicate path %s", record.StorageKey)
		}
		paths[record.StorageKey] = true
		first = record
	}
	if len(paths) != count {
		t.Fatalf("published %d of %d", len(paths), count)
	}
	entries, err := os.ReadDir(filepath.Join(root, "scan"))
	if err != nil || len(entries) != count {
		t.Fatalf("staging directories leaked: %d, %v", len(entries), err)
	}
	// Relative keys survive moving the store; stale absolute metadata is ignored.
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(root, relocated); err != nil {
		t.Fatal(err)
	}
	if err := Init(relocated); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArtifact(*first); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../escape", "scan/../gate-result.json", "/outside"} {
		invalid := *first
		invalid.StorageKey = key
		if data, err := ReadArtifact(invalid); err == nil || len(data) != 0 {
			t.Fatalf("invalid key accepted: %s", key)
		}
	}
	invalid := *first
	invalid.FileSize++
	if data, err := ReadArtifact(invalid); !errors.Is(err, ErrIntegrity) || len(data) != 0 {
		t.Fatalf("wrong size accepted: %q %v", data, err)
	}
	path := filepath.Join(relocated, filepath.FromSlash(first.StorageKey))
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(first.FileSize))), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadArtifact(*first); !errors.Is(err, ErrIntegrity) || len(data) != 0 {
		t.Fatalf("corruption accepted: %q %v", data, err)
	}
	// Legacy rows remain readable, without claiming verification.
	legacy := *first
	legacy.ChecksumSHA256 = ""
	if _, err := ReadArtifact(legacy); err != nil {
		t.Fatal(err)
	}
}

type failingSnapshotReader struct{}

func (failingSnapshotReader) Read(p []byte) (int, error) {
	return copy(p, "partial"), io.ErrUnexpectedEOF
}

func TestPublishFailureAndConfinement(t *testing.T) {
	previous := Global
	t.Cleanup(func() { Global = previous })
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if record, err := Publish(t.Context(), "scan", "output", failingSnapshotReader{}); record != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial publish: %+v, %v", record, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "scan"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial output leaked: %+v %v", entries, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if record, err := Publish(ctx, "scan", "output", strings.NewReader("data")); record != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publish: %+v %v", record, err)
	}
	for _, name := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "bad\x00name"} {
		for _, pair := range [][2]string{{name, "output"}, {"scan", name}} {
			if record, err := Publish(t.Context(), pair[0], pair[1], strings.NewReader("data")); record != nil || err == nil {
				t.Fatalf("invalid components accepted: %q", pair)
			}
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if record, err := Publish(t.Context(), "escape", "output", strings.NewReader("data")); record != nil || err == nil {
		t.Fatalf("escaped publication: %+v %v", record, err)
	}
	entries, err = os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside writes: %+v %v", entries, err)
	}
	Global = nil
	if record, err := Publish(t.Context(), "scan", "output", strings.NewReader("data")); record != nil || err == nil {
		t.Fatalf("uninitialized publication: %+v %v", record, err)
	}
}
