package swift

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// SwiftLintPlugin runs SwiftLint for Swift code analysis.
type SwiftLintPlugin struct{}

func init() {
	plugin.Register(&SwiftLintPlugin{})
}

func (p *SwiftLintPlugin) Name() string             { return "swiftlint" }
func (p *SwiftLintPlugin) Category() models.Category { return models.CategoryQuality }
func (p *SwiftLintPlugin) Languages() []models.Language {
	return []models.Language{models.LangSwift}
}

func (p *SwiftLintPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("swiftlint")
	return err == nil
}

func (p *SwiftLintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "swiftlint", "lint", "--reporter", "json", "--path", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("swiftlint execution failed: %w", err)
		}
	}

	return parseSwiftLintOutput(out)
}

type swiftlintViolation struct {
	RuleID   string `json:"rule_id"`
	Reason   string `json:"reason"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Type     string `json:"type"`
}

func parseSwiftLintOutput(data []byte) ([]models.Finding, error) {
	var violations []swiftlintViolation
	if err := json.Unmarshal(data, &violations); err != nil {
		return nil, fmt.Errorf("failed to parse swiftlint output: %w", err)
	}

	findings := make([]models.Finding, 0, len(violations))
	for _, v := range violations {
		findings = append(findings, models.Finding{
			ToolName:    "swiftlint",
			Category:    models.CategoryQuality,
			Severity:    mapSwiftLintSeverity(v.Severity),
			Title:       v.RuleID,
			Description: v.Reason,
			FilePath:    v.File,
			LineStart:   v.Line,
			RuleID:      v.RuleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapSwiftLintSeverity(s string) models.Severity {
	switch s {
	case "Error":
		return models.SeverityHigh
	case "Warning":
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}
