package python

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// RuffPlugin runs Ruff linter for Python.
type RuffPlugin struct{}

func init() {
	plugin.Register(&RuffPlugin{})
}

func (p *RuffPlugin) Name() string               { return "ruff" }
func (p *RuffPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *RuffPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *RuffPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("ruff")
	return err == nil
}

func (p *RuffPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "py") {
		plugin.Skipf(opts.OnOutput, "ruff", "no Python files (*.py) found. Add Python source files to enable linting.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "ruff", "check", opts.RepoPath, "--output-format", "json")
	out, err := cmd.Output()
	if err != nil {
		// Ruff exits non-zero when findings exist; only fail if no output.
		if len(out) == 0 {
			return nil, plugin.WrapExecError("ruff", err)
		}
	}

	return parseRuffOutput(out)
}

type ruffResult struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Filename string `json:"filename"`
	Location struct {
		Row int `json:"row"`
	} `json:"location"`
	EndLocation struct {
		Row int `json:"row"`
	} `json:"end_location"`
	URL string `json:"url"`
}

func parseRuffOutput(data []byte) ([]models.Finding, error) {
	var results []ruffResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse ruff output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, models.Finding{
			ToolName:    "ruff",
			Category:    models.CategoryQuality,
			Severity:    mapRuffSeverity(r.Code),
			Title:       fmt.Sprintf("%s: %s", r.Code, r.Message),
			Description: r.Message,
			FilePath:    r.Filename,
			LineStart:   r.Location.Row,
			LineEnd:     r.EndLocation.Row,
			RuleID:      r.Code,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapRuffSeverity(code string) models.Severity {
	if strings.HasPrefix(code, "F") || strings.HasPrefix(code, "E9") {
		return models.SeverityHigh
	}
	if strings.HasPrefix(code, "E") || strings.HasPrefix(code, "W") {
		return models.SeverityMedium
	}
	return models.SeverityLow
}
