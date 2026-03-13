package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// CodeQLPlugin runs CodeQL SAST analysis with SARIF output.
type CodeQLPlugin struct{}

func init() {
	plugin.Register(&CodeQLPlugin{})
}

func (p *CodeQLPlugin) Name() string               { return "codeql" }
func (p *CodeQLPlugin) Category() models.Category   { return models.CategorySAST }
func (p *CodeQLPlugin) Languages() []models.Language { return nil }

func (p *CodeQLPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("codeql")
	return err == nil
}

func (p *CodeQLPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// CodeQL requires a database; we use the SARIF output format
	args := []string{"database", "analyze", opts.RepoPath, "--format=sarif-latest", "--output=/dev/stdout"}
	cmd := exec.CommandContext(ctx, "codeql", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("codeql execution failed: %w", err)
		}
	}

	return parseCodeQLOutput(out)
}

// SARIF types for CodeQL output
type sarifOutput struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool struct {
		Driver struct {
			Rules []sarifRule `json:"rules"`
		} `json:"driver"`
	} `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifRule struct {
	ID               string `json:"id"`
	ShortDescription struct {
		Text string `json:"text"`
	} `json:"shortDescription"`
	DefaultConfiguration struct {
		Level string `json:"level"`
	} `json:"defaultConfiguration"`
	Properties struct {
		Tags []string `json:"tags"`
	} `json:"properties"`
}

type sarifResult struct {
	RuleID    string `json:"ruleId"`
	RuleIndex int    `json:"ruleIndex"`
	Message   struct {
		Text string `json:"text"`
	} `json:"message"`
	Level     string `json:"level"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine   int `json:"startLine"`
				EndLine     int `json:"endLine"`
				StartColumn int `json:"startColumn"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

func parseCodeQLOutput(data []byte) ([]models.Finding, error) {
	var sarif sarifOutput
	if err := json.Unmarshal(data, &sarif); err != nil {
		return nil, fmt.Errorf("failed to parse codeql SARIF output: %w", err)
	}

	var findings []models.Finding
	for _, run := range sarif.Runs {
		ruleMap := make(map[string]sarifRule)
		for _, rule := range run.Tool.Driver.Rules {
			ruleMap[rule.ID] = rule
		}

		for _, result := range run.Results {
			filePath := ""
			lineStart, lineEnd := 0, 0
			if len(result.Locations) > 0 {
				loc := result.Locations[0].PhysicalLocation
				filePath = loc.ArtifactLocation.URI
				lineStart = loc.Region.StartLine
				lineEnd = loc.Region.EndLine
			}

			level := result.Level
			if level == "" {
				if r, ok := ruleMap[result.RuleID]; ok {
					level = r.DefaultConfiguration.Level
				}
			}

			findings = append(findings, models.Finding{
				ToolName:    "codeql",
				Category:    models.CategorySAST,
				Severity:    mapSARIFSeverity(level),
				Title:       result.RuleID,
				Description: result.Message.Text,
				FilePath:    filePath,
				LineStart:   lineStart,
				LineEnd:     lineEnd,
				RuleID:      result.RuleID,
				SARIFData:   string(data),
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapSARIFSeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
