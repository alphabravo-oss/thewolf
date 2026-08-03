package docs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// SpectralPlugin runs Spectral API spec linting.
type SpectralPlugin struct{}

func init() {
	plugin.Register(&SpectralPlugin{})
}

func (p *SpectralPlugin) Name() string                 { return "spectral" }
func (p *SpectralPlugin) Category() models.Category    { return models.CategoryDocs }
func (p *SpectralPlugin) Languages() []models.Language { return nil }

func (p *SpectralPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *SpectralPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	hasSpec := plugin.HasFile(opts.RepoPath, "openapi.yaml") ||
		plugin.HasFile(opts.RepoPath, "openapi.yml") ||
		plugin.HasFile(opts.RepoPath, "openapi.json") ||
		plugin.HasFile(opts.RepoPath, "swagger.yaml") ||
		plugin.HasFile(opts.RepoPath, "swagger.yml") ||
		plugin.HasFile(opts.RepoPath, "swagger.json")
	if !hasSpec {
		plugin.Skipf(opts.OnOutput, "spectral", "no OpenAPI/Swagger spec files found. Add an API specification file to enable linting.")
		return nil, nil
	}

	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"spectral", "lint", "/scan", "-f", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("spectral", err)
	}

	findings, perr := parseSpectralOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
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
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
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
