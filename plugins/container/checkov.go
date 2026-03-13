package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// CheckovPlugin runs Checkov IaC security scanning.
type CheckovPlugin struct{}

func init() {
	plugin.Register(&CheckovPlugin{})
}

func (p *CheckovPlugin) Name() string               { return "checkov" }
func (p *CheckovPlugin) Category() models.Category   { return models.CategoryContainer }
func (p *CheckovPlugin) Languages() []models.Language { return nil }

func (p *CheckovPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("checkov")
	return err == nil
}

func (p *CheckovPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"-d", opts.RepoPath, "-o", "json", "--quiet"}
	cmd := exec.CommandContext(ctx, "checkov", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("checkov execution failed: %w", err)
		}
	}

	return parseCheckovOutput(out)
}

type checkovOutput struct {
	Results checkovResults `json:"results"`
}

type checkovResults struct {
	FailedChecks []checkovCheck `json:"failed_checks"`
}

type checkovCheck struct {
	CheckID      string   `json:"check_id"`
	CheckResult  struct {
		Result string `json:"result"`
	} `json:"check_result"`
	Name         string   `json:"name"`
	FilePath     string   `json:"file_path"`
	FileLineRange []int   `json:"file_line_range"`
	Guideline    string   `json:"guideline"`
	Severity     string   `json:"severity"`
	CodeBlock    [][]interface{} `json:"code_block"`
}

func parseCheckovOutput(data []byte) ([]models.Finding, error) {
	// Checkov can return an array or object
	var output checkovOutput
	if err := json.Unmarshal(data, &output); err != nil {
		// Try as array (multiple frameworks)
		var outputs []checkovOutput
		if err2 := json.Unmarshal(data, &outputs); err2 != nil {
			return nil, fmt.Errorf("failed to parse checkov output: %w", err)
		}
		var findings []models.Finding
		for _, o := range outputs {
			f, _ := convertCheckovFindings(o.Results.FailedChecks)
			findings = append(findings, f...)
		}
		return findings, nil
	}

	return convertCheckovFindings(output.Results.FailedChecks)
}

func convertCheckovFindings(checks []checkovCheck) ([]models.Finding, error) {
	findings := make([]models.Finding, 0, len(checks))
	for _, c := range checks {
		lineStart, lineEnd := 0, 0
		if len(c.FileLineRange) >= 2 {
			lineStart = int(c.FileLineRange[0])
			lineEnd = int(c.FileLineRange[1])
		}

		findings = append(findings, models.Finding{
			ToolName:    "checkov",
			Category:    models.CategoryContainer,
			Severity:    mapCheckovSeverity(c.Severity),
			Title:       fmt.Sprintf("[%s] %s", c.CheckID, c.Name),
			Description: c.Guideline,
			FilePath:    c.FilePath,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			RuleID:      c.CheckID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapCheckovSeverity(s string) models.Severity {
	switch s {
	case "CRITICAL":
		return models.SeverityCritical
	case "HIGH":
		return models.SeverityHigh
	case "MEDIUM":
		return models.SeverityMedium
	case "LOW":
		return models.SeverityLow
	default:
		return models.SeverityMedium
	}
}
