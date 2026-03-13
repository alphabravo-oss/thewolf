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

// RadonPlugin runs Radon cyclomatic complexity analysis for Python.
type RadonPlugin struct{}

func init() {
	plugin.Register(&RadonPlugin{})
}

func (p *RadonPlugin) Name() string               { return "radon" }
func (p *RadonPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *RadonPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *RadonPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("radon")
	return err == nil
}

func (p *RadonPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "py") {
		plugin.Skipf(opts.OnOutput, "radon", "no Python files (*.py) found. Add Python source files to enable complexity analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "radon", "cc", opts.RepoPath, "-j")
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("radon", err)
		}
	}

	return parseRadonOutput(out)
}

type radonBlock struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	ClassName  string `json:"classname"`
	LineNo     int    `json:"lineno"`
	EndLine    int    `json:"endline"`
	Complexity int    `json:"complexity"`
	Rank       string `json:"rank"`
}

func parseRadonOutput(data []byte) ([]models.Finding, error) {
	var output map[string][]radonBlock
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse radon output: %w", err)
	}

	var findings []models.Finding
	for filePath, blocks := range output {
		for _, b := range blocks {
			if b.Complexity <= 10 {
				continue
			}

			name := b.Name
			if b.ClassName != "" {
				name = b.ClassName + "." + b.Name
			}

			findings = append(findings, models.Finding{
				ToolName:    "radon",
				Category:    models.CategoryQuality,
				Severity:    mapRadonSeverity(b.Rank),
				Title:       fmt.Sprintf("High complexity in %s %s (complexity: %d, rank: %s)", b.Type, name, b.Complexity, b.Rank),
				Description: fmt.Sprintf("Cyclomatic complexity of %d (rank %s) detected in %s %q. Consider refactoring to reduce complexity.", b.Complexity, b.Rank, b.Type, name),
				FilePath:    filePath,
				LineStart:   b.LineNo,
				LineEnd:     b.EndLine,
				RuleID:      fmt.Sprintf("CC-%s", b.Rank),
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapRadonSeverity(rank string) models.Severity {
	switch strings.ToUpper(rank) {
	case "A", "B":
		return models.SeverityInfo
	case "C":
		return models.SeverityLow
	case "D":
		return models.SeverityMedium
	case "E", "F":
		return models.SeverityHigh
	default:
		return models.SeverityInfo
	}
}
