package security

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

type genericSARIF struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Rules []struct {
					ID                   string                 `json:"id"`
					ShortDescription     struct{ Text string }  `json:"shortDescription"`
					DefaultConfiguration struct{ Level string } `json:"defaultConfiguration"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine int `json:"startLine"`
						EndLine   int `json:"endLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func parseSARIFFindings(tool string, category models.Category, data []byte) ([]models.Finding, error) {
	var doc genericSARIF
	if err := json.Unmarshal(plugin.ExtractJSON(data), &doc); err != nil {
		return nil, fmt.Errorf("%s: parse sarif: %w", tool, err)
	}
	var findings []models.Finding
	for _, run := range doc.Runs {
		titles := map[string]string{}
		levels := map[string]string{}
		for _, rule := range run.Tool.Driver.Rules {
			titles[rule.ID] = rule.ShortDescription.Text
			levels[rule.ID] = rule.DefaultConfiguration.Level
		}
		for _, result := range run.Results {
			filePath, lineStart, lineEnd := "", 0, 0
			if len(result.Locations) > 0 {
				loc := result.Locations[0].PhysicalLocation
				filePath = loc.ArtifactLocation.URI
				lineStart = loc.Region.StartLine
				lineEnd = loc.Region.EndLine
			}
			level := result.Level
			if level == "" {
				level = levels[result.RuleID]
			}
			title := titles[result.RuleID]
			if title == "" {
				title = result.RuleID
			}
			findings = append(findings, models.Finding{
				ToolName:    tool,
				Category:    category,
				Severity:    mapGenericSARIFSeverity(level),
				Title:       title,
				Description: result.Message.Text,
				FilePath:    filePath,
				LineStart:   lineStart,
				LineEnd:     lineEnd,
				RuleID:      result.RuleID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapGenericSARIFSeverity(level string) models.Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note", "info":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
