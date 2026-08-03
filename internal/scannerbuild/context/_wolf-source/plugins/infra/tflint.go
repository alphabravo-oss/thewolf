package infra

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// TFLintPlugin runs TFLint for Terraform files.
type TFLintPlugin struct{}

func init() {
	plugin.Register(&TFLintPlugin{})
}

func (p *TFLintPlugin) Name() string                 { return "tflint" }
func (p *TFLintPlugin) Category() models.Category    { return models.CategoryInfra }
func (p *TFLintPlugin) Languages() []models.Language { return nil }

func (p *TFLintPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *TFLintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "tf") {
		plugin.Skipf(opts.OnOutput, "tflint", "no Terraform files (*.tf) found. Add .tf files to enable infrastructure linting.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath, WorkDir: "/scan"},
		"tflint", "--format", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("tflint", err)
	}

	findings, perr := parseTFLintOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
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
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
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
