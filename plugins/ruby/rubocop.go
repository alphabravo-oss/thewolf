package ruby

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// RubocopPlugin runs RuboCop static analysis for Ruby code.
type RubocopPlugin struct{}

func init() {
	plugin.Register(&RubocopPlugin{})
}

func (p *RubocopPlugin) Name() string             { return "rubocop" }
func (p *RubocopPlugin) Category() models.Category { return models.CategoryQuality }
func (p *RubocopPlugin) Languages() []models.Language {
	return []models.Language{models.LangRuby}
}

func (p *RubocopPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("rubocop")
	return err == nil
}

func (p *RubocopPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "rb") {
		plugin.Skipf(opts.OnOutput, "rubocop", "no Ruby files (*.rb) found. Add Ruby source files to enable linting.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "rubocop", "--format", "json", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("rubocop", err)
		}
	}

	return parseRubocopOutput(out)
}

type rubocopOutput struct {
	Files []rubocopFile `json:"files"`
}

type rubocopFile struct {
	Path     string           `json:"path"`
	Offenses []rubocopOffense `json:"offenses"`
}

type rubocopOffense struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	CopName  string `json:"cop_name"`
	Location struct {
		StartLine   int `json:"start_line"`
		StartColumn int `json:"start_column"`
		LastLine    int `json:"last_line"`
	} `json:"location"`
}

func parseRubocopOutput(data []byte) ([]models.Finding, error) {
	var output rubocopOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse rubocop output: %w", err)
	}

	var findings []models.Finding
	for _, f := range output.Files {
		for _, o := range f.Offenses {
			findings = append(findings, models.Finding{
				ToolName:    "rubocop",
				Category:    models.CategoryQuality,
				Severity:    mapRubocopSeverity(o.Severity),
				Title:       o.CopName,
				Description: o.Message,
				FilePath:    f.Path,
				LineStart:   o.Location.StartLine,
				LineEnd:     o.Location.LastLine,
				RuleID:      o.CopName,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapRubocopSeverity(s string) models.Severity {
	switch s {
	case "fatal", "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "convention", "refactor":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
