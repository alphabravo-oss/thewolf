package general

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// GrypePlugin runs Grype vulnerability scanning.
type GrypePlugin struct{}

func init() {
	plugin.Register(&GrypePlugin{})
}

func (p *GrypePlugin) Name() string               { return "grype" }
func (p *GrypePlugin) Category() models.Category   { return models.CategorySCA }
func (p *GrypePlugin) Languages() []models.Language { return nil }

func (p *GrypePlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *GrypePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// grype caches its ~1.5GB vuln database. We point it at the
	// wolf-shared DBVolume mounted at /var/lib/wolf-db (a named Docker
	// volume that persists across scan runs), with a sub-namespace per
	// tool. First run downloads (~30s); subsequent runs reuse the cache.
	// Falling back to /tmp keeps things working when no DBVolume is
	// configured, at the cost of re-downloading every run.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			ExtraEnv: map[string]string{
				"HOME":               "/tmp",
				"XDG_CACHE_HOME":     "/var/lib/wolf-db",
				"GRYPE_DB_CACHE_DIR": "/var/lib/wolf-db/grype",
			},
			// grype loads its full vulnerability DB into memory before
			// matching. 6g leaves headroom for the DB + scanning state.
			MemoryOverride: "6g",
		},
		"grype", "dir:/scan", "-o", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("grype", err)
	}

	findings, perr := parseGrypeOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
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
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
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
