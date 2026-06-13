// Package diff classifies findings between a baseline scan and a current scan.
package diff

import (
	"sort"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	StateNew        = "new"
	StateExisting   = "existing"
	StateFixed      = "fixed"
	StateResurfaced = "resurfaced"
)

type Result struct {
	New        []models.Finding `json:"new"`
	Existing   []models.Finding `json:"existing"`
	Fixed      []models.Finding `json:"fixed"`
	Resurfaced []models.Finding `json:"resurfaced"`
}

type Counts struct {
	New        int `json:"new"`
	Existing   int `json:"existing"`
	Fixed      int `json:"fixed"`
	Resurfaced int `json:"resurfaced"`
}

func (r Result) Counts() Counts {
	return Counts{
		New:        len(r.New),
		Existing:   len(r.Existing),
		Fixed:      len(r.Fixed),
		Resurfaced: len(r.Resurfaced),
	}
}

// Compare classifies current findings relative to baseline findings by stable
// fingerprint. If a finding lacks durable identity, Compare computes it in
// memory without mutating the caller's slice.
func Compare(baseline, current []models.Finding) Result {
	baseByID := make(map[string]models.Finding, len(baseline))
	for _, f := range baseline {
		key := keyFor(f)
		if key == "" {
			continue
		}
		if _, exists := baseByID[key]; !exists {
			baseByID[key] = f
		}
	}

	seenCurrent := make(map[string]bool, len(current))
	var result Result
	for _, f := range current {
		key := keyFor(f)
		if key == "" {
			f.BaselineState = StateNew
			result.New = append(result.New, f)
			continue
		}
		seenCurrent[key] = true
		if _, exists := baseByID[key]; exists {
			f.BaselineState = StateExisting
			result.Existing = append(result.Existing, f)
		} else {
			f.BaselineState = StateNew
			result.New = append(result.New, f)
		}
	}

	for key, f := range baseByID {
		if seenCurrent[key] {
			continue
		}
		f.BaselineState = StateFixed
		result.Fixed = append(result.Fixed, f)
	}

	sortFindings(result.New)
	sortFindings(result.Existing)
	sortFindings(result.Fixed)
	sortFindings(result.Resurfaced)
	return result
}

func keyFor(f models.Finding) string {
	if f.StableFingerprint != "" {
		return f.StableFingerprint
	}
	if f.Fingerprint != "" {
		return f.Fingerprint
	}
	fps := identity.Build(f)
	return fps.Stable
}

func sortFindings(findings []models.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].FilePath != findings[j].FilePath {
			return findings[i].FilePath < findings[j].FilePath
		}
		if findings[i].LineStart != findings[j].LineStart {
			return findings[i].LineStart < findings[j].LineStart
		}
		if findings[i].RuleID != findings[j].RuleID {
			return findings[i].RuleID < findings[j].RuleID
		}
		return findings[i].Title < findings[j].Title
	})
}
