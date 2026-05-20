package manifest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIterationManifest_Delta(t *testing.T) {
	m := IterationManifest{
		FingerprintsIn:  []string{"a", "b", "c"},
		FingerprintsOut: []string{"b", "c", "d"},
	}
	d := m.Delta()
	if strings.Join(d.Fixed, ",") != "a" {
		t.Errorf("Fixed = %v, want [a]", d.Fixed)
	}
	if strings.Join(d.Remaining, ",") != "b,c" {
		t.Errorf("Remaining = %v, want [b c]", d.Remaining)
	}
	if strings.Join(d.New, ",") != "d" {
		t.Errorf("New = %v, want [d]", d.New)
	}
}

func TestRunManifest_Write(t *testing.T) {
	dir := t.TempDir()
	r := &RunManifest{
		LoopID:    "loop-1",
		ScanID:    "scan-1",
		FixBranch: "wolf/fix-scan-1",
		StartedAt: time.Now(),
	}
	r.Add(IterationManifest{
		LoopID:          "loop-1",
		Iteration:       1,
		GitSHABefore:    "aaa",
		GitSHAAfter:     "bbb",
		ScannerDigests:  map[string]string{"trivy": "sha256:1"},
		FingerprintsIn:  []string{"x", "y"},
		FingerprintsOut: []string{"y"},
		AITool:          "claude-code",
	})

	path, err := r.Write(dir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back RunManifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(back.Iterations) != 1 || back.Iterations[0].AITool != "claude-code" {
		t.Errorf("round-trip lost data: %+v", back)
	}
	// The recorded iteration's delta reconstructs the fix.
	if d := back.Iterations[0].Delta(); strings.Join(d.Fixed, ",") != "x" {
		t.Errorf("delta from recorded manifest wrong: %+v", d)
	}
}

func TestWriteIteration(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteIteration(dir, IterationManifest{LoopID: "l", Iteration: 3})
	if err != nil {
		t.Fatalf("WriteIteration: %v", err)
	}
	if !strings.HasSuffix(path, "iteration-3.json") {
		t.Errorf("unexpected path %q", path)
	}
}
