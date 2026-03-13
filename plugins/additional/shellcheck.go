package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// ShellcheckPlugin runs ShellCheck shell script analysis.
type ShellcheckPlugin struct{}

func init() {
	plugin.Register(&ShellcheckPlugin{})
}

func (p *ShellcheckPlugin) Name() string             { return "shellcheck" }
func (p *ShellcheckPlugin) Category() models.Category { return models.CategoryQuality }
func (p *ShellcheckPlugin) Languages() []models.Language {
	return []models.Language{models.LangShell}
}

func (p *ShellcheckPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("shellcheck")
	return err == nil
}

func (p *ShellcheckPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Find shell files in the repo
	shellFiles, err := filepath.Glob(filepath.Join(opts.RepoPath, "**", "*.sh"))
	if err != nil {
		return nil, fmt.Errorf("failed to find shell files: %w", err)
	}
	rootShellFiles, _ := filepath.Glob(filepath.Join(opts.RepoPath, "*.sh"))
	shellFiles = append(shellFiles, rootShellFiles...)

	if len(shellFiles) == 0 {
		plugin.Skipf(opts.OnOutput, "shellcheck", "no shell scripts (*.sh) found in repository. Add .sh files to enable shell script analysis.")
		return nil, nil
	}

	args := append([]string{"-f", "json"}, shellFiles...)
	cmd := plugin.CommandContext(ctx, "shellcheck", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("shellcheck", err)
		}
	}

	return parseShellcheckOutput(out)
}

type shellcheckResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine"`
	Column  int    `json:"column"`
	Level   string `json:"level"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Fix     *struct {
		Replacements []struct {
			Line       int    `json:"line"`
			Column     int    `json:"column"`
			Replacement string `json:"replacement"`
		} `json:"replacements"`
	} `json:"fix"`
}

func parseShellcheckOutput(data []byte) ([]models.Finding, error) {
	var results []shellcheckResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse shellcheck output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		ruleID := fmt.Sprintf("SC%d", r.Code)
		findings = append(findings, models.Finding{
			ToolName:    "shellcheck",
			Category:    models.CategoryQuality,
			Severity:    mapShellcheckSeverity(r.Level),
			Title:       fmt.Sprintf("[%s] %s", ruleID, r.Message),
			Description: r.Message,
			FilePath:    r.File,
			LineStart:   r.Line,
			LineEnd:     r.EndLine,
			RuleID:      ruleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapShellcheckSeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "info":
		return models.SeverityLow
	case "style":
		return models.SeverityInfo
	default:
		return models.SeverityInfo
	}
}
