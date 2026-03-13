package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// SQLFluffPlugin runs SQLFluff linting on SQL files.
type SQLFluffPlugin struct{}

func init() {
	plugin.Register(&SQLFluffPlugin{})
}

func (p *SQLFluffPlugin) Name() string             { return "sqlfluff" }
func (p *SQLFluffPlugin) Category() models.Category { return models.CategoryQuality }
func (p *SQLFluffPlugin) Languages() []models.Language {
	return []models.Language{models.LangSQL}
}

func (p *SQLFluffPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("sqlfluff")
	return err == nil
}

func (p *SQLFluffPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "sql") {
		plugin.Skipf(opts.OnOutput, "sqlfluff", "no SQL files (*.sql) found. Add SQL files to enable linting.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "sqlfluff", "lint", "--format", "json", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("sqlfluff", err)
		}
	}

	return parseSQLFluffOutput(out)
}

type sqlfluffResult struct {
	Filepath   string             `json:"filepath"`
	Violations []sqlfluffViolation `json:"violations"`
}

type sqlfluffViolation struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	LineNo      int    `json:"start_line_no"`
	LinePos     int    `json:"start_line_pos"`
}

func parseSQLFluffOutput(data []byte) ([]models.Finding, error) {
	var results []sqlfluffResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse sqlfluff output: %w", err)
	}

	var findings []models.Finding
	for _, r := range results {
		for _, v := range r.Violations {
			findings = append(findings, models.Finding{
				ToolName:    "sqlfluff",
				Category:    models.CategoryQuality,
				Severity:    models.SeverityLow,
				Title:       fmt.Sprintf("[%s] %s", v.Code, v.Description),
				Description: v.Description,
				FilePath:    r.Filepath,
				LineStart:   v.LineNo,
				RuleID:      v.Code,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
