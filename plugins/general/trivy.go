package general

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// TrivyPlugin runs Trivy vulnerability scanning.
type TrivyPlugin struct{}

func init() {
	plugin.Register(&TrivyPlugin{})
}

func (p *TrivyPlugin) Name() string               { return "trivy" }
func (p *TrivyPlugin) Category() models.Category   { return models.CategorySCA }
func (p *TrivyPlugin) Languages() []models.Language { return nil }

func (p *TrivyPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("trivy")
	return err == nil
}

func (p *TrivyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"fs", "--format", "json", opts.RepoPath}
	cmd := plugin.CommandContext(ctx, "trivy", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("trivy", err)
		}
	}

	return parseTrivyOutput(out)
}

type trivyOutput struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Target          string             `json:"Target"`
	Vulnerabilities []trivyVulnerability `json:"Vulnerabilities"`
}

type trivyVulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	Description      string `json:"Description"`
}

func parseTrivyOutput(data []byte) ([]models.Finding, error) {
	var output trivyOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse trivy output: %w", err)
	}

	var findings []models.Finding
	for _, result := range output.Results {
		for _, vuln := range result.Vulnerabilities {
			desc := vuln.Description
			if vuln.FixedVersion != "" {
				desc += fmt.Sprintf(" (fix available: %s)", vuln.FixedVersion)
			}
			findings = append(findings, models.Finding{
				ToolName:    "trivy",
				Category:    models.CategorySCA,
				Severity:    mapTrivySeverity(vuln.Severity),
				Title:       fmt.Sprintf("%s in %s@%s", vuln.VulnerabilityID, vuln.PkgName, vuln.InstalledVersion),
				Description: desc,
				FilePath:    result.Target,
				RuleID:      vuln.VulnerabilityID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapTrivySeverity(s string) models.Severity {
	switch s {
	case "CRITICAL":
		return models.SeverityCritical
	case "HIGH":
		return models.SeverityHigh
	case "MEDIUM":
		return models.SeverityMedium
	case "LOW":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
