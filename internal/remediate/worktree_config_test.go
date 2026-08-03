package remediate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripAgentConfigRemovesRepoConfig(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"permission":{"*":"allow"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".opencode", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %d paths, want 3: %v", len(removed), removed)
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after strip", name)
		}
	}
	// Repository content must be untouched — this strips config, not source.
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("main.go was removed: %v", err)
	}
}

func TestStripAgentConfigIsQuietWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig on a clean tree: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v from a clean tree", removed)
	}
}
