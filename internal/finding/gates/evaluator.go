// Package gates evaluates normalized findings against deterministic quality
// gate policies.
package gates

import (
	"encoding/json"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"

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
	ID            string            `json:"id"`
	Description   string            `json:"description,omitempty"`
	Severity      []models.Severity `json:"severity,omitempty"`
	Category      []models.Category `json:"category,omitempty"`
	FineCategory  []string          `json:"fine_category,omitempty"`
	BaselineState []string          `json:"baseline_state,omitempty"`
	ConfidenceMin string            `json:"confidence_min,omitempty"`
	Suppressed    *bool             `json:"suppressed,omitempty"`
	Action        string            `json:"action"`
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
	Status       string        `json:"status"`
	Summary      Summary       `json:"summary"`
	MatchedRules []MatchedRule `json:"matched_rules"`
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
				Category:   securityCategories(),
				Suppressed: &notSuppressed,
				Action:     ActionFail,
			},
			{
				ID:         "warn-medium-security",
				Severity:   []models.Severity{models.SeverityMedium},
				Category:   securityCategories(),
				Suppressed: &notSuppressed,
				Action:     ActionWarn,
			},
		},
	}
}

func ParsePolicy(name, mode, rulesJSON string) (Policy, error) {
	var rules []Rule
	if strings.TrimSpace(rulesJSON) != "" {
		if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
			return Policy{}, err
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

	for _, rule := range policy.Rules {
		matchedIDs := make([]string, 0)
		for _, finding := range findings {
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

	return Evaluation{Status: summary.Status, Summary: summary, MatchedRules: matches}
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
	if len(rule.BaselineState) > 0 && f.BaselineState != "" && !containsString(rule.BaselineState, f.BaselineState) {
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

func securityCategories() []models.Category {
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
