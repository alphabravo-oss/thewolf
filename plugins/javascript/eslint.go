package javascript

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// ESLintPlugin runs ESLint for JavaScript and TypeScript quality checks.
type ESLintPlugin struct{}

func init() {
	plugin.Register(&ESLintPlugin{})
}

func (p *ESLintPlugin) Name() string             { return "eslint" }
func (p *ESLintPlugin) Category() models.Category { return models.CategoryQuality }
func (p *ESLintPlugin) Languages() []models.Language {
	return []models.Language{models.LangJavaScript, models.LangTypeScript}
}

func (p *ESLintPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("eslint")
	return err == nil
}

func (p *ESLintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "eslint", opts.RepoPath, "-f", "json")
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("eslint execution failed: %w", err)
		}
	}

	return parseESLintOutput(out)
}

type eslintFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

type eslintMessage struct {
	RuleID   string `json:"ruleId"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Line     int    `json:"line"`
	EndLine  int    `json:"endLine"`
}

func parseESLintOutput(data []byte) ([]models.Finding, error) {
	var files []eslintFile
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, fmt.Errorf("failed to parse eslint output: %w", err)
	}

	var findings []models.Finding
	for _, f := range files {
		for _, m := range f.Messages {
			findings = append(findings, models.Finding{
				ToolName:    "eslint",
				Category:    models.CategoryQuality,
				Severity:    mapESLintSeverity(m.Severity),
				Title:       m.RuleID,
				Description: m.Message,
				FilePath:    f.FilePath,
				LineStart:   m.Line,
				LineEnd:     m.EndLine,
				RuleID:      m.RuleID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapESLintSeverity(s int) models.Severity {
	switch s {
	case 2:
		return models.SeverityHigh
	case 1:
		return models.SeverityMedium
	default:
		return models.SeverityInfo
	}
}
