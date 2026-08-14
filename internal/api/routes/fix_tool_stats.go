package routes

import (
	"sort"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ScannerToolStat is "this scanner reported N findings; we fixed K of them".
type ScannerToolStat struct {
	Tool      string `json:"tool"`
	Total     int    `json:"total"`
	Kept      int    `json:"kept"`
	Open      int    `json:"open"`
	Unfixable int    `json:"unfixable"`
	Muted     int    `json:"muted"`
	Rolled    int    `json:"rolled"`
	After     int    `json:"after"`
}

func scannerToolStats(findings []models.Finding, attempts []models.FixAttempt) []ScannerToolStat {
	latest := map[string]string{}
	for _, a := range attempts {
		if a.FindingID == "" {
			continue
		}
		latest[a.FindingID] = a.Outcome
	}
	index := map[string]int{}
	var out []ScannerToolStat
	for _, f := range findings {
		tool := f.ToolName
		if tool == "" {
			tool = "unknown"
		}
		i, ok := index[tool]
		if !ok {
			i = len(out)
			index[tool] = i
			out = append(out, ScannerToolStat{Tool: tool})
		}
		out[i].Total++
		switch latest[f.ID] {
		case models.FixOutcomeKept:
			out[i].Kept++
		case models.FixOutcomeMuted:
			out[i].Muted++
		case models.FixOutcomeUnfixable:
			out[i].Unfixable++
		case models.FixOutcomeRolledBack:
			out[i].Rolled++
			out[i].Open++
		default:
			out[i].Open++
		}
	}
	for i := range out {
		out[i].After = out[i].Total - out[i].Kept - out[i].Muted
		if out[i].After < 0 {
			out[i].After = 0
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

func annotateAttemptTools(attempts []models.FixAttempt, findings []models.Finding) {
	byID := make(map[string]models.Finding, len(findings))
	for _, f := range findings {
		byID[f.ID] = f
	}
	for i := range attempts {
		f, ok := byID[attempts[i].FindingID]
		if !ok {
			continue
		}
		attempts[i].ToolName = f.ToolName
		attempts[i].Title = f.Title
		attempts[i].FilePath = f.FilePath
		attempts[i].LineStart = f.LineStart
		attempts[i].Severity = string(f.Severity)
		attempts[i].RuleID = f.RuleID
	}
}

func findingsInJob(all []models.Finding, job *models.FixJob) []models.Finding {
	if job == nil || len(job.FindingIDList) == 0 {
		return all
	}
	want := make(map[string]bool, len(job.FindingIDList))
	for _, id := range job.FindingIDList {
		want[id] = true
	}
	out := make([]models.Finding, 0, len(job.FindingIDList))
	for _, f := range all {
		if want[f.ID] {
			out = append(out, f)
		}
	}
	return out
}
