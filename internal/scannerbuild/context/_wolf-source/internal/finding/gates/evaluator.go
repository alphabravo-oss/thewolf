// Package gates evaluates normalized findings against deterministic quality
// gate policies.
package gates

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	StatusPass       = "pass"
	StatusWarn       = "warn"
	StatusFail       = "fail"
	StatusPending    = "pending"
	StatusIncomplete = "incomplete"

	ActionFail   = "fail"
	ActionWarn   = "warn"
	ActionIgnore = "ignore"
)

type Policy struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Rules []Rule `json:"rules"`
}

type Rule struct {
	ID            string               `json:"id"`
	Description   string               `json:"description,omitempty"`
	Severity      []models.Severity    `json:"severity,omitempty"`
	Category      []models.Category    `json:"category,omitempty"`
	FineCategory  []string             `json:"fine_category,omitempty"`
	BaselineState []string             `json:"baseline_state,omitempty"`
	ConfidenceMin string               `json:"confidence_min,omitempty"`
	Suppressed    *bool                `json:"suppressed,omitempty"`
	Action        string               `json:"action"`
	TestCoverage  *CoverageRequirement `json:"test_coverage,omitempty"`
}

type MatchedRule struct {
	RuleID       string   `json:"rule_id"`
	Action       string   `json:"action"`
	FindingIDs   []string `json:"finding_ids"`
	FindingCount int      `json:"finding_count"`
}

type Summary struct {
	Status     string         `json:"status"`
	FailCount  int            `json:"fail_count"`
	WarnCount  int            `json:"warn_count"`
	PassCount  int            `json:"pass_count"`
	Ignored    int            `json:"ignored"`
	BySeverity map[string]int `json:"by_severity"`
}

type Evaluation struct {
	Status       string          `json:"status"`
	Reason       string          `json:"reason,omitempty"`
	Summary      Summary         `json:"summary"`
	MatchedRules []MatchedRule   `json:"matched_rules"`
	Coverage     []CoverageCheck `json:"coverage,omitempty"`
}

// EvaluateScan never treats missing scanner evidence as a clean scan.
// Evaluate remains available for callers evaluating a known findings dataset.
func EvaluateScan(policy Policy, scan *models.Scan, findings []models.Finding) Evaluation {
	eval := Evaluate(policy, findings)
	status, reason := ScanCompleteness(scan)
	if status != "complete" {
		eval.Status, eval.Summary.Status, eval.Reason = status, status, reason
	}
	return eval
}

// ScanCompleteness is shared by quality gates and scan status responses.
func ScanCompleteness(scan *models.Scan) (string, string) {
	status, reason := "complete", "All selected scanners completed successfully."
	switch scan.Status {
	case models.ScanStatusPending, models.ScanStatusRunning:
		status, reason = StatusPending, "Scan has not finished."
	case models.ScanStatusFailed, models.ScanStatusCancelled:
		status, reason = StatusIncomplete, "Scan "+string(scan.Status)+"; results are incomplete."
	case models.ScanStatusCompleted:
		var selected, completed, failed []string
		if json.Unmarshal([]byte(scan.ToolsSelected), &selected) != nil ||
			json.Unmarshal([]byte(scan.ToolsCompleted), &completed) != nil ||
			json.Unmarshal([]byte(scan.ToolsFailed), &failed) != nil {
			status, reason = StatusIncomplete, "Scanner completion metadata is invalid."
		} else if len(selected) == 0 || len(completed) == 0 || len(failed) > 0 {
			status, reason = StatusIncomplete, "Not all selected scanners completed successfully."
		} else {
			for _, tool := range selected {
				if tool == "" || !slices.Contains(completed, tool) {
					status, reason = StatusIncomplete, "Not all selected scanners completed successfully."
					break
				}
			}
		}
	default:
		status, reason = StatusIncomplete, "Scan execution status is unknown."
	}
	return status, reason
}

func DefaultPolicy() Policy {
	notSuppressed := false
	return Policy{
		Name: "default-security-gate",
		Mode: "warn",
		Rules: []Rule{
			{
				ID:         "fail-unsuppressed-secrets",
				Category:   []models.Category{models.CategorySecrets},
				Suppressed: &notSuppressed,
				Action:     ActionFail,
			},
			{
				ID:           "fail-hardcoded-secret",
				FineCategory: []string{"hardcoded-secret"},
				Suppressed:   &notSuppressed,
				Action:       ActionFail,
			},
			{
				ID:         "fail-critical",
				Severity:   []models.Severity{models.SeverityCritical},
				Suppressed: &notSuppressed,
				Action:     ActionFail,
			},
			{
				ID:         "fail-high-security",
				Severity:   []models.Severity{models.SeverityHigh},
				Category:   SecurityCategories(),
				Suppressed: &notSuppressed,
				Action:     ActionFail,
			},
			{
				ID:         "warn-medium-security",
				Severity:   []models.Severity{models.SeverityMedium},
				Category:   SecurityCategories(),
				Suppressed: &notSuppressed,
				Action:     ActionWarn,
			},
		},
	}
}

func FastPRPolicy() Policy {
	p := DefaultPolicy()
	p.Name = "fast-pr-gate"
	for i := range p.Rules {
		switch p.Rules[i].ID {
		case "fail-critical", "fail-high-security":
			p.Rules[i].BaselineState = []string{"new", "resurfaced"}
		}
	}
	return p
}

func ParsePolicy(name, mode, rulesJSON string) (Policy, error) {
	var rules []Rule
	if strings.TrimSpace(rulesJSON) != "" {
		if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
			return Policy{}, err
		}
	}
	for _, rule := range rules {
		if c := rule.TestCoverage; c != nil {
			if (c.Scope != "" && c.Scope != "overall" && c.Scope != "changed") || (c.Scope == "changed" && c.Metric != "lines") || strings.Contains(c.PathPrefix, "..") || strings.HasPrefix(c.PathPrefix, "/") || strings.ContainsAny(c.PathPrefix, "\\:\x00\r\n") {
				return Policy{}, fmt.Errorf("coverage scope must be overall or changed; changed coverage requires lines; path_prefix must be repository-relative")
			}
			if rule.ID == "" || (rule.Action != ActionFail && rule.Action != ActionWarn) ||
				!slices.Contains([]string{"lines", "branches", "statements", "functions"}, c.Metric) ||
				math.IsNaN(c.Minimum) || math.IsInf(c.Minimum, 0) || c.Minimum < 0 || c.Minimum > 100 {
				return Policy{}, fmt.Errorf("invalid test coverage rule: metric must be lines, branches, statements or functions; minimum must be 0–100; action must be fail or warn")
			}
			if len(rule.Severity)+len(rule.Category)+len(rule.FineCategory)+len(rule.BaselineState) > 0 || rule.Suppressed != nil || rule.ConfidenceMin != "" {
				return Policy{}, fmt.Errorf("test coverage rules cannot contain finding filters")
			}
		}
	}
	return Policy{Name: name, Mode: mode, Rules: rules}, nil
}

func Evaluate(policy Policy, findings []models.Finding) Evaluation {
	if len(policy.Rules) == 0 {
		policy = DefaultPolicy()
	}
	summary := Summary{
		Status:     StatusPass,
		BySeverity: make(map[string]int),
	}
	matches := make([]MatchedRule, 0)
	unknownBaseline := false

	for _, rule := range policy.Rules {
		if rule.TestCoverage != nil {
			continue
		}
		matchedIDs := make([]string, 0)
		for _, finding := range findings {
			if len(rule.BaselineState) > 0 && finding.BaselineState == "" && rule.Action != ActionIgnore {
				withoutBaseline := rule
				withoutBaseline.BaselineState = nil
				if matchesRule(withoutBaseline, finding) {
					unknownBaseline = true
				}
			}
			if !matchesRule(rule, finding) {
				continue
			}
			if rule.Action == ActionIgnore {
				summary.Ignored++
				continue
			}
			matchedIDs = append(matchedIDs, finding.ID)
			summary.BySeverity[string(finding.Severity)]++
		}
		if len(matchedIDs) == 0 {
			continue
		}
		switch rule.Action {
		case ActionFail:
			summary.FailCount += len(matchedIDs)
		case ActionWarn:
			summary.WarnCount += len(matchedIDs)
		default:
			summary.PassCount += len(matchedIDs)
		}
		matches = append(matches, MatchedRule{
			RuleID:       rule.ID,
			Action:       rule.Action,
			FindingIDs:   matchedIDs,
			FindingCount: len(matchedIDs),
		})
	}

	switch {
	case summary.FailCount > 0:
		summary.Status = StatusFail
	case summary.WarnCount > 0:
		summary.Status = StatusWarn
	default:
		summary.Status = StatusPass
	}

	eval := Evaluation{Status: summary.Status, Summary: summary, MatchedRules: matches}
	if unknownBaseline && eval.Status != StatusFail {
		eval.Status, eval.Summary.Status = StatusIncomplete, StatusIncomplete
		eval.Reason = "Baseline state is unknown for findings covered by the policy."
	}
	return eval
}

func matchesRule(rule Rule, f models.Finding) bool {
	if rule.ID == "" {
		return false
	}
	if len(rule.Severity) > 0 && !containsSeverity(rule.Severity, f.Severity) {
		return false
	}
	if len(rule.Category) > 0 && !containsCategory(rule.Category, f.Category) {
		return false
	}
	if len(rule.FineCategory) > 0 && !containsString(rule.FineCategory, f.FineCategory) {
		return false
	}
	if len(rule.BaselineState) > 0 && !containsString(rule.BaselineState, f.BaselineState) {
		return false
	}
	if rule.ConfidenceMin != "" && confidenceRank(f.Confidence) < confidenceRank(rule.ConfidenceMin) {
		return false
	}
	if rule.Suppressed != nil && f.Suppressed != *rule.Suppressed {
		return false
	}
	return true
}

func SecurityCategories() []models.Category {
	return []models.Category{
		models.CategorySAST,
		models.CategorySCA,
		models.CategorySecrets,
		models.CategoryContainer,
		models.CategoryInfra,
		models.CategoryDAST,
	}
}

func containsSeverity(values []models.Severity, needle models.Severity) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsCategory(values []models.Category, needle models.Category) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func confidenceRank(confidence string) int {
	switch strings.ToLower(confidence) {
	case "confirmed":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
