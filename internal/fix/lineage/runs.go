package lineage

import (
	"encoding/json"
	"sort"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Run is one agent job on a remediation, paired with the child scan it
// produced so the scan page can show finding-count deltas.
type Run struct {
	JobID          string `json:"job_id"`
	Status         string `json:"status"`
	RunIndex       int    `json:"run_index"`
	PlannedRuns    int    `json:"planned_runs"`
	CreatedAt      string `json:"created_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	InputScanID    string `json:"input_scan_id,omitempty"`
	InputFindings  int    `json:"input_findings"`
	ChildScanID    string `json:"child_scan_id,omitempty"`
	ChildStatus    string `json:"child_status,omitempty"`
	OutputFindings *int   `json:"output_findings,omitempty"`
	Delta          *int   `json:"delta,omitempty"`
	Kept           int    `json:"kept"`
	Muted          int    `json:"muted"`
	Unfixable      int    `json:"unfixable"`
	Remaining      int    `json:"remaining"`
	Pushed         bool   `json:"pushed,omitempty"`
	PushSHA        string `json:"push_sha,omitempty"`
	ResultBranch   string `json:"result_branch,omitempty"`
	PauseReason    string `json:"pause_reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

type summaryLite struct {
	Kept          int `json:"kept"`
	Muted         int `json:"muted"`
	Unfixable     int `json:"unfixable"`
	Remaining     int `json:"remaining"`
	TotalFindings int `json:"total_findings"`
}

// BuildRuns pairs agent jobs with the child scans they spawned, oldest first.
func BuildRuns(origin *models.Scan, children []models.Scan, agents []models.FixJob) []Run {
	if origin == nil {
		return nil
	}
	scans := map[string]models.Scan{origin.ID: *origin}
	childByJob := map[string]models.Scan{}
	for _, s := range children {
		scans[s.ID] = s
		if s.FixJobID != "" {
			childByJob[s.FixJobID] = s
		}
	}
	jobs := append([]models.FixJob(nil), agents...)
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	out := make([]Run, 0, len(jobs))
	for _, j := range jobs {
		run := Run{
			JobID:        j.ID,
			Status:       j.Status,
			RunIndex:     j.RunIndex,
			PlannedRuns:  j.PlannedRuns,
			InputScanID:  j.ScanID,
			Pushed:       j.Pushed,
			PushSHA:      j.PushSHA,
			ResultBranch: firstNonEmpty(j.ResultBranch, j.TargetBranch),
			PauseReason:  j.PauseReason,
			Error:        j.Error,
		}
		if run.RunIndex <= 0 {
			run.RunIndex = 1
		}
		if run.PlannedRuns <= 0 {
			run.PlannedRuns = 1
		}
		if !j.CreatedAt.IsZero() {
			run.CreatedAt = j.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if j.FinishedAt != nil {
			run.FinishedAt = j.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if in, ok := scans[j.ScanID]; ok {
			run.InputFindings = in.FindingCount
		} else if j.ScanID == origin.ID || j.ScanID == "" {
			run.InputFindings = origin.FindingCount
			if run.InputScanID == "" {
				run.InputScanID = origin.ID
			}
		}
		var lite summaryLite
		if j.Summary != "" && j.Summary != "{}" {
			_ = json.Unmarshal([]byte(j.Summary), &lite)
			run.Kept = lite.Kept
			run.Muted = lite.Muted
			run.Unfixable = lite.Unfixable
			run.Remaining = lite.Remaining
			if run.InputFindings == 0 && lite.TotalFindings > 0 {
				run.InputFindings = lite.TotalFindings
			}
		}
		if child, ok := childByJob[j.ID]; ok {
			run.ChildScanID = child.ID
			run.ChildStatus = string(child.Status)
			if child.Status == models.ScanStatusCompleted {
				n := child.FindingCount
				run.OutputFindings = &n
				d := n - run.InputFindings
				run.Delta = &d
			}
		}
		out = append(out, run)
	}
	return out
}
