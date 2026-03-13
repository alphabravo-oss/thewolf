package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// TFLintPlugin runs TFLint for Terraform files.
type TFLintPlugin struct{}

func init() {
	plugin.Register(&TFLintPlugin{})
}

func (p *TFLintPlugin) Name() string             { return "tflint" }
func (p *TFLintPlugin) Category() models.Category { return models.CategoryInfra }
func (p *TFLintPlugin) Languages() []models.Language { return nil }

func (p *TFLintPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("tflint")
	return err == nil
}

func (p *TFLintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "tflint", "--format", "json")
	cmd.Dir = opts.RepoPath
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("tflint execution failed: %w", err)
		}
	}

	return parseTFLintOutput(out)
}

type tflintOutput struct {
	Issues []tflintIssue `json:"issues"`
}

type tflintIssue struct {
	Rule struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
	} `json:"rule"`
	Message string `json:"message"`
	Range   struct {
		Filename string `json:"filename"`
		Start    struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
		End struct {
			Line int `json:"line"`
		} `json:"end"`
	} `json:"range"`
}

func parseTFLintOutput(data []byte) ([]models.Finding, error) {
	var output tflintOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse tflint output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Issues))
	for _, issue := range output.Issues {
		findings = append(findings, models.Finding{
			ToolName:    "tflint",
			Category:    models.CategoryInfra,
			Severity:    mapTFLintSeverity(issue.Rule.Severity),
			Title:       issue.Rule.Name,
			Description: issue.Message,
			FilePath:    issue.Range.Filename,
			LineStart:   issue.Range.Start.Line,
			LineEnd:     issue.Range.End.Line,
			RuleID:      issue.Rule.Name,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapTFLintSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "notice":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
