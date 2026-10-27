package workerstatus

import (
	"os"
	"path/filepath"
	"testing"
)

// The worker publishes engine readiness and its prompt defaults; the API reads
// both from this file without linking the fixer engines.
func TestWriteReadStatus(t *testing.T) {
	dir := t.TempDir()
	if got, err := Read(dir); err != nil || got.Engines != nil || got.Prompts != nil {
		t.Fatalf("missing file should be empty, got %+v / %v", got, err)
	}
	want := Status{
		Engines:     []EngineStatus{{Name: "api", Available: true, Auth: "api_key"}},
		Prompts:     map[string]string{"fixer_prompt_initial": "Fix {{findings_file}}"},
		Placeholder: "{{findings_file}}",
	}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, FileName)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("status file: %v %v", info, err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Engines) != 1 || got.Engines[0].Auth != "api_key" || got.Prompts["fixer_prompt_initial"] != "Fix {{findings_file}}" || got.Placeholder != "{{findings_file}}" || got.UpdatedAt == "" {
		t.Fatalf("got %+v", got)
	}
}
