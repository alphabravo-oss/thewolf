// Package tracker provides finding diff tracking across loop iterations.
// It uses stable fingerprints to identify findings that persist, appear,
// or disappear between scan iterations.
package tracker

import (
	"sync"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// IterationDiff represents the difference in findings between two iterations.
type IterationDiff struct {
	Fixed          []models.Finding
	New            []models.Finding
	Remaining      []models.Finding
	FixedCount     int
	NewCount       int
	RemainingCount int
}

// Tracker tracks findings by fingerprint across loop iterations.
type Tracker struct {
	mu         sync.Mutex
	iterations map[int]map[string]models.Finding // iteration -> fingerprint -> finding
}

// New creates a new Tracker.
func New() *Tracker {
	return &Tracker{
		iterations: make(map[int]map[string]models.Finding),
	}
}

// Update records findings for a given iteration. Findings are indexed by
// their Fingerprint field for stable cross-iteration comparison.
func (t *Tracker) Update(iteration int, findings []models.Finding) {
	t.mu.Lock()
	defer t.mu.Unlock()

	m := make(map[string]models.Finding, len(findings))
	for _, f := range findings {
		if f.Fingerprint != "" {
			m[f.Fingerprint] = f
		}
	}
	t.iterations[iteration] = m
}

// Diff computes the difference between two iterations. Fixed findings are
// those present in prevIter but absent in currIter. New findings are present
// in currIter but absent in prevIter. Remaining findings exist in both.
func (t *Tracker) Diff(prevIter, currIter int) *IterationDiff {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev := t.iterations[prevIter]
	curr := t.iterations[currIter]

	diff := &IterationDiff{}

	// Fixed: in prev but not in curr.
	for fp, f := range prev {
		if _, exists := curr[fp]; !exists {
			diff.Fixed = append(diff.Fixed, f)
		}
	}

	// New: in curr but not in prev.
	for fp, f := range curr {
		if _, exists := prev[fp]; !exists {
			diff.New = append(diff.New, f)
		}
	}

	// Remaining: in both.
	for fp, f := range curr {
		if _, exists := prev[fp]; exists {
			diff.Remaining = append(diff.Remaining, f)
		}
	}

	diff.FixedCount = len(diff.Fixed)
	diff.NewCount = len(diff.New)
	diff.RemainingCount = len(diff.Remaining)

	return diff
}

// FindingsForIteration returns the findings recorded for a given iteration.
func (t *Tracker) FindingsForIteration(iteration int) []models.Finding {
	t.mu.Lock()
	defer t.mu.Unlock()

	m, ok := t.iterations[iteration]
	if !ok {
		return nil
	}

	findings := make([]models.Finding, 0, len(m))
	for _, f := range m {
		findings = append(findings, f)
	}
	return findings
}
