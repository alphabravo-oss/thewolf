package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// SpectralPlugin runs Spectral API spec linting.
type SpectralPlugin struct{}

func init() {
	plugin.Register(&SpectralPlugin{})
}

func (p *SpectralPlugin) Name() string               { return "spectral" }
func (p *SpectralPlugin) Category() models.Category   { return models.CategoryDocs }
func (p *SpectralPlugin) Languages() []models.Language { return nil }

func (p *SpectralPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("spectral")
	return err == nil
}

func (p *SpectralPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"lint", opts.RepoPath, "-f", "json"}
	cmd := exec.CommandContext(ctx, "spectral", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("spectral execution failed: %w", err)
		}
	}

	return parseSpectralOutput(out)
}

type spectralResult struct {
	Code     interface{} `json:"code"`
	Path     []string    `json:"path"`
	Message  string      `json:"message"`
	Severity int         `json:"severity"`
	Range    struct {
		Start struct {
			Line int `json:"line"`
		} `json:"start"`
		End struct {
			Line int `json:"line"`
		} `json:"end"`
	} `json:"range"`
	Source string `json:"source"`
}

func parseSpectralOutput(data []byte) ([]models.Finding, error) {
	var results []spectralResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse spectral output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		ruleID := fmt.Sprintf("%v", r.Code)
		findings = append(findings, models.Finding{
			ToolName:    "spectral",
			Category:    models.CategoryDocs,
			Severity:    mapSpectralSeverity(r.Severity),
			Title:       fmt.Sprintf("[%s] %s", ruleID, r.Message),
			Description: r.Message,
			FilePath:    r.Source,
			LineStart:   r.Range.Start.Line + 1, // Spectral uses 0-based lines
			LineEnd:     r.Range.End.Line + 1,
			RuleID:      ruleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapSpectralSeverity(severity int) models.Severity {
	switch severity {
	case 0:
		return models.SeverityHigh
	case 1:
		return models.SeverityMedium
	case 2:
		return models.SeverityLow
	case 3:
		return models.SeverityInfo
	default:
		return models.SeverityInfo
	}
}
