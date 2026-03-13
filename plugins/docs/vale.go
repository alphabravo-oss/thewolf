package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// ValePlugin runs Vale documentation style checking.
type ValePlugin struct{}

func init() {
	plugin.Register(&ValePlugin{})
}

func (p *ValePlugin) Name() string               { return "vale" }
func (p *ValePlugin) Category() models.Category   { return models.CategoryDocs }
func (p *ValePlugin) Languages() []models.Language { return nil }

func (p *ValePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("vale")
	return err == nil
}

func (p *ValePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "md", "rst", "txt") {
		plugin.Skipf(opts.OnOutput, "vale", "no documentation files (*.md, *.rst) found. Add Markdown or reStructuredText files to enable prose linting.")
		return nil, nil
	}

	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"--output", "JSON", opts.RepoPath}
	cmd := plugin.CommandContext(ctx, "vale", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("vale", err)
		}
	}

	return parseValeOutput(out)
}

// Vale outputs: { "file.md": [ { ... }, ... ] }
type valeAlert struct {
	Action struct {
		Name   string `json:"name"`
		Params []string `json:"params"`
	} `json:"Action"`
	Check    string `json:"Check"`
	Line     int    `json:"Line"`
	Link     string `json:"Link"`
	Message  string `json:"Message"`
	Severity string `json:"Severity"`
	Span     []int  `json:"Span"`
	Match    string `json:"Match"`
}

func parseValeOutput(data []byte) ([]models.Finding, error) {
	var output map[string][]valeAlert
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse vale output: %w", err)
	}

	var findings []models.Finding
	for filePath, alerts := range output {
		for _, a := range alerts {
			findings = append(findings, models.Finding{
				ToolName:    "vale",
				Category:    models.CategoryDocs,
				Severity:    mapValeSeverity(a.Severity),
				Title:       fmt.Sprintf("[%s] %s", a.Check, a.Message),
				Description: a.Message,
				FilePath:    filePath,
				LineStart:   a.Line,
				CodeSnippet: a.Match,
				RuleID:      a.Check,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapValeSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "suggestion":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
