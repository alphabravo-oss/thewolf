package general

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// GrypePlugin runs Grype vulnerability scanning.
type GrypePlugin struct{}

func init() {
	plugin.Register(&GrypePlugin{})
}

func (p *GrypePlugin) Name() string               { return "grype" }
func (p *GrypePlugin) Category() models.Category   { return models.CategorySCA }
func (p *GrypePlugin) Languages() []models.Language { return nil }

func (p *GrypePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("grype")
	return err == nil
}

func (p *GrypePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"dir:" + opts.RepoPath, "-o", "json"}
	cmd := exec.CommandContext(ctx, "grype", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("grype execution failed: %w", err)
		}
	}

	return parseGrypeOutput(out)
}

type grypeOutput struct {
	Matches []grypeMatch `json:"matches"`
}

type grypeMatch struct {
	Vulnerability grypeVuln    `json:"vulnerability"`
	Artifact      grypeArtifact `json:"artifact"`
}

type grypeVuln struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Fix         struct {
		Versions []string `json:"versions"`
	} `json:"fix"`
}

type grypeArtifact struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Locations []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

func parseGrypeOutput(data []byte) ([]models.Finding, error) {
	var output grypeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse grype output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Matches))
	for _, m := range output.Matches {
		filePath := ""
		if len(m.Artifact.Locations) > 0 {
			filePath = m.Artifact.Locations[0].Path
		}

		desc := m.Vulnerability.Description
		if len(m.Vulnerability.Fix.Versions) > 0 {
			desc += fmt.Sprintf(" (fix: %s)", m.Vulnerability.Fix.Versions[0])
		}

		findings = append(findings, models.Finding{
			ToolName:    "grype",
			Category:    models.CategorySCA,
			Severity:    mapGrypeSeverity(m.Vulnerability.Severity),
			Title:       fmt.Sprintf("%s in %s@%s", m.Vulnerability.ID, m.Artifact.Name, m.Artifact.Version),
			Description: desc,
			FilePath:    filePath,
			RuleID:      m.Vulnerability.ID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapGrypeSeverity(s string) models.Severity {
	switch s {
	case "Critical":
		return models.SeverityCritical
	case "High":
		return models.SeverityHigh
	case "Medium":
		return models.SeverityMedium
	case "Low":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
