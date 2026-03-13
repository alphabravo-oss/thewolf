package report

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	sarifSchema  = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/main/sarif-2.1/schema/sarif-schema-2.1.0.json"
	sarifVersion = "2.1.0"
)

// SARIF top-level types.

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string              `json:"id"`
	ShortDescription sarifMessage        `json:"shortDescription"`
	Properties       *sarifRuleProperties `json:"properties,omitempty"`
}

type sarifRuleProperties struct {
	CWEID string `json:"cweId,omitempty"`
}

type sarifResult struct {
	RuleID    string           `json:"ruleId"`
	Level     string           `json:"level"`
	Message   sarifMessage     `json:"message"`
	Locations []sarifLocation  `json:"locations,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine,omitempty"`
}

// severityToSARIFLevel maps Wolf severity to SARIF level.
func severityToSARIFLevel(sev models.Severity) string {
	switch sev {
	case models.SeverityCritical, models.SeverityHigh:
		return "error"
	case models.SeverityMedium:
		return "warning"
	case models.SeverityLow, models.SeverityInfo:
		return "note"
	default:
		return "note"
	}
}

// renderSARIF builds a SARIF v2.1.0 log from the report config.
// Each tool that produced findings becomes its own run.
func renderSARIF(cfg ReportConfig) ([]byte, error) {
	byTool := findingsByTool(cfg.Findings)

	// Gather all tool names (including those with zero findings) and sort.
	toolNames := make(map[string]bool)
	for _, t := range cfg.ToolsRun {
		toolNames[t] = true
	}
	for t := range byTool {
		toolNames[t] = true
	}
	sorted := make([]string, 0, len(toolNames))
	for t := range toolNames {
		sorted = append(sorted, t)
	}
	sort.Strings(sorted)

	runs := make([]sarifRun, 0, len(sorted))
	for _, toolName := range sorted {
		findings := byTool[toolName]
		if len(findings) == 0 {
			// Only include runs that have results.
			continue
		}

		rulesMap := make(map[string]sarifRule)
		var results []sarifResult

		for _, f := range findings {
			ruleID := f.RuleID
			if ruleID == "" {
				ruleID = fmt.Sprintf("%s/%s", f.ToolName, f.ID)
			}

			// Register rule if not seen.
			if _, exists := rulesMap[ruleID]; !exists {
				rule := sarifRule{
					ID:               ruleID,
					ShortDescription: sarifMessage{Text: f.Title},
				}
				if f.CWEID != "" {
					rule.Properties = &sarifRuleProperties{CWEID: f.CWEID}
				}
				rulesMap[ruleID] = rule
			}

			result := sarifResult{
				RuleID:  ruleID,
				Level:   severityToSARIFLevel(f.Severity),
				Message: sarifMessage{Text: f.Description},
			}

			if f.FilePath != "" {
				loc := sarifLocation{
					PhysicalLocation: sarifPhysicalLocation{
						ArtifactLocation: sarifArtifactLocation{URI: f.FilePath},
					},
				}
				if f.LineStart > 0 {
					loc.PhysicalLocation.Region = &sarifRegion{
						StartLine: f.LineStart,
						EndLine:   f.LineEnd,
					}
				}
				result.Locations = []sarifLocation{loc}
			}

			results = append(results, result)
		}

		// Deterministic rule order.
		ruleIDs := make([]string, 0, len(rulesMap))
		for id := range rulesMap {
			ruleIDs = append(ruleIDs, id)
		}
		sort.Strings(ruleIDs)
		rules := make([]sarifRule, 0, len(ruleIDs))
		for _, id := range ruleIDs {
			rules = append(rules, rulesMap[id])
		}

		runs = append(runs, sarifRun{
			Tool: sarifTool{
				Driver: sarifDriver{
					Name:  toolName,
					Rules: rules,
				},
			},
			Results: results,
		})
	}

	log := sarifLog{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs:    runs,
	}

	return json.MarshalIndent(log, "", "  ")
}
