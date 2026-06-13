// Package manifest records a per-iteration audit trail for an
// auto-remediation loop. AI output is never bit-reproducible, so
// determinism here means: every loop run is fully reconstructable from
// the recorded manifests — what code state went in, which scanners (by
// digest) produced the findings, which fingerprints cleared, which AI
// tool ran, and where the AI's plan artifact lives.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// IterationManifest is the audit record for a single loop iteration.
type IterationManifest struct {
	LoopID    string `json:"loop_id"`
	Iteration int    `json:"iteration"`

	// Git state bracketing the iteration's AI edits.
	GitSHABefore string `json:"git_sha_before"`
	GitSHAAfter  string `json:"git_sha_after"`

	// ScannerDigests pins the scanner images used so finding deltas are
	// attributable to AI edits, not scanner drift.
	ScannerDigests map[string]string `json:"scanner_digests,omitempty"`
	ToolSet        []string          `json:"tool_set,omitempty"`

	// Finding fingerprints before and after the iteration's rescan.
	FingerprintsIn  []string `json:"fingerprints_in"`
	FingerprintsOut []string `json:"fingerprints_out"`

	// AITool is the engine that ran this iteration.
	AITool string `json:"ai_tool"`
	// PlanArtifact is the path of the AI's written plan, relative to the
	// loop's artifact directory.
	PlanArtifact string `json:"plan_artifact,omitempty"`

	// StopReason is set on the iteration that ended the loop.
	StopReason string `json:"stop_reason,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// FindingDelta classifies fingerprint movement across the iteration.
type FindingDelta struct {
	Fixed     []string `json:"fixed"`
	Remaining []string `json:"remaining"`
	New       []string `json:"new"`
}

// Delta compares the in/out fingerprint sets. A fingerprint present
// before but not after is "fixed"; present both is "remaining"; present
// only after is "new".
func (m IterationManifest) Delta() FindingDelta {
	in := toSet(m.FingerprintsIn)
	out := toSet(m.FingerprintsOut)
	var d FindingDelta
	for fp := range in {
		if out[fp] {
			d.Remaining = append(d.Remaining, fp)
		} else {
			d.Fixed = append(d.Fixed, fp)
		}
	}
	for fp := range out {
		if !in[fp] {
			d.New = append(d.New, fp)
		}
	}
	sort.Strings(d.Fixed)
	sort.Strings(d.Remaining)
	sort.Strings(d.New)
	return d
}

// RunManifest aggregates every iteration of one loop run.
type RunManifest struct {
	LoopID     string              `json:"loop_id"`
	ScanID     string              `json:"scan_id"`
	RepoPath   string              `json:"repo_path"`
	Branch     string              `json:"branch"`
	FixBranch  string              `json:"fix_branch"`
	StartedAt  time.Time           `json:"started_at"`
	FinishedAt time.Time           `json:"finished_at"`
	Iterations []IterationManifest `json:"iterations"`
}

// Add appends an iteration record.
func (r *RunManifest) Add(it IterationManifest) {
	r.Iterations = append(r.Iterations, it)
}

// Write serializes the run manifest to <dir>/loop-manifest.json.
func (r *RunManifest) Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create manifest dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	path := filepath.Join(dir, "loop-manifest.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return path, nil
}

// WriteIteration serializes a single iteration manifest to
// <dir>/iteration-<n>.json — handy for streaming progress.
func WriteIteration(dir string, it IterationManifest) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create manifest dir: %w", err)
	}
	data, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode iteration manifest: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("iteration-%d.json", it.Iteration))
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write iteration manifest: %w", err)
	}
	return path, nil
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		if s != "" {
			m[s] = true
		}
	}
	return m
}
