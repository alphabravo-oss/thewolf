package coverage

import (
	"encoding/json"
	"fmt"
	"sort"
)

type ChangedCoverage struct {
	BaseCommitSHA string         `json:"base_commit_sha"`
	Metric        Metric         `json:"metric"`
	ChangedLines  int            `json:"changed_lines"`
	Files         []MeasuredFile `json:"files,omitempty"`
}

// ReadDiffCover reuses diff-cover's Git comparison, and verifies every reported
// line against the full coverage bundle. The CI uploader supplies base/head
// provenance, just as it supplies the underlying execution report.
func ReadDiffCover(raw json.RawMessage, base string, report *MeasuredReport) (*ChangedCoverage, error) {
	var input struct {
		Stats map[string]struct {
			Covered []int `json:"covered_lines"`
			Missed  []int `json:"violation_lines"`
		} `json:"src_stats"`
		Total   *int `json:"total_num_lines"`
		Missed  *int `json:"total_num_violations"`
		Changed *int `json:"num_changed_lines"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if input.Stats == nil || input.Total == nil || input.Missed == nil || input.Changed == nil {
		return nil, fmt.Errorf("expected diff-cover JSON with source line details and totals")
	}
	files := map[string]MeasuredFile{}
	for _, f := range report.Files {
		files[f.Path] = f
	}
	out := &ChangedCoverage{BaseCommitSHA: base, ChangedLines: *input.Changed}
	seen := map[string]bool{}
	for name, stats := range input.Stats {
		p, err := reportPath(name, ReportInput{})
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate normalized diff-cover path %s", p)
		}
		seen[p] = true
		original, ok := files[p]
		if !ok {
			return nil, fmt.Errorf("diff-cover file %s is absent from the full coverage bundle", p)
		}
		file := MeasuredFile{Path: p, Metrics: map[string]Metric{}, Lines: map[int]bool{}}
		metric := Metric{}
		for _, group := range []struct {
			lines   []int
			covered bool
		}{{stats.Covered, true}, {stats.Missed, false}} {
			for _, line := range group.lines {
				covered, exists := original.Lines[line]
				if !exists || covered != group.covered {
					return nil, fmt.Errorf("diff-cover line %s:%d does not match the full report", p, line)
				}
				if _, exists := file.Lines[line]; exists {
					return nil, fmt.Errorf("duplicate diff-cover line")
				}
				file.Lines[line] = covered
				metric.Total++
				if covered {
					metric.Covered++
				}
			}
		}
		file.Metrics["lines"] = metric
		out.Files = append(out.Files, file)
		out.Metric.Total += metric.Total
		out.Metric.Covered += metric.Covered
	}
	if out.Metric.Total != *input.Total || out.Metric.Total-out.Metric.Covered != *input.Missed || out.ChangedLines < out.Metric.Total {
		return nil, fmt.Errorf("diff-cover totals do not match source line details")
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}
