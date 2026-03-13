package ruby

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// BrakemanPlugin runs Brakeman security scanning for Ruby on Rails apps.
type BrakemanPlugin struct{}

func init() {
	plugin.Register(&BrakemanPlugin{})
}

func (p *BrakemanPlugin) Name() string             { return "brakeman" }
func (p *BrakemanPlugin) Category() models.Category { return models.CategorySAST }
func (p *BrakemanPlugin) Languages() []models.Language {
	return []models.Language{models.LangRuby}
}

func (p *BrakemanPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("brakeman")
	return err == nil
}

func (p *BrakemanPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFile(opts.RepoPath, "Gemfile") {
		plugin.Skipf(opts.OnOutput, "brakeman", "no Gemfile found — not a Ruby project. Brakeman requires a Rails/Ruby application.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "brakeman", "-f", "json", "-q", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("brakeman", err)
		}
	}

	return parseBrakemanOutput(out)
}

type brakemanOutput struct {
	Warnings []brakemanWarning `json:"warnings"`
}

type brakemanWarning struct {
	WarningType string `json:"warning_type"`
	WarningCode int    `json:"warning_code"`
	Message     string `json:"message"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Link        string `json:"link"`
	Code        string `json:"code"`
	Confidence  string `json:"confidence"`
	CWE         []int  `json:"cwe_id"`
}

func parseBrakemanOutput(data []byte) ([]models.Finding, error) {
	var output brakemanOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse brakeman output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Warnings))
	for _, w := range output.Warnings {
		cwe := ""
		if len(w.CWE) > 0 {
			cwe = fmt.Sprintf("CWE-%d", w.CWE[0])
		}

		findings = append(findings, models.Finding{
			ToolName:    "brakeman",
			Category:    models.CategorySAST,
			Severity:    mapBrakemanConfidence(w.Confidence),
			Title:       w.WarningType,
			Description: w.Message,
			FilePath:    w.File,
			LineStart:   w.Line,
			CodeSnippet: w.Code,
			CWEID:       cwe,
			RuleID:      fmt.Sprintf("brakeman-%d", w.WarningCode),
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapBrakemanConfidence(c string) models.Severity {
	switch c {
	case "High":
		return models.SeverityHigh
	case "Medium":
		return models.SeverityMedium
	case "Weak":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
