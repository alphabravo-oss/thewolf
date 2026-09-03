package security

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// OSVScannerPlugin runs Google's OSV-Scanner for dependency vulnerability scanning.
type OSVScannerPlugin struct{}

func init() {
	plugin.Register(&OSVScannerPlugin{})
}

func (p *OSVScannerPlugin) Name() string                 { return "osv-scanner" }
func (p *OSVScannerPlugin) Category() models.Category    { return models.CategorySCA }
func (p *OSVScannerPlugin) Languages() []models.Language { return nil }

func (p *OSVScannerPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *OSVScannerPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"osv-scanner", "--format", "json", "--recursive", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("osv-scanner", err)
	}

	findings, perr := parseOSVScannerOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type osvOutput struct {
	Results []osvResult `json:"results"`
}

type osvResult struct {
	Source struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"source"`
	Packages []osvPackageResult `json:"packages"`
}

type osvPackageResult struct {
	Package struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Vulnerabilities []osvVuln `json:"vulnerabilities"`
}

type osvVuln struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Aliases []string `json:"aliases"`
}

func parseOSVScannerOutput(data []byte) ([]models.Finding, error) {
	var output osvOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse osv-scanner output: %w", err)
	}

	var findings []models.Finding
	for _, r := range output.Results {
		for _, pkg := range r.Packages {
			for _, v := range pkg.Vulnerabilities {
				cve := ""
				for _, alias := range v.Aliases {
					if strings.HasPrefix(alias, "CVE-") {
						cve = alias
						break
					}
				}

				findings = append(findings, models.Finding{
					ToolName:    "osv-scanner",
					Category:    models.CategorySCA,
					Severity:    mapOSVSeverity(v.Severity),
					Title:       fmt.Sprintf("%s: %s", v.ID, v.Summary),
					Description: fmt.Sprintf("Package: %s@%s (%s)\n\n%s", pkg.Package.Name, pkg.Package.Version, pkg.Package.Ecosystem, v.Details),
					FilePath:    r.Source.Path,
					CWEID:       cve,
					RuleID:      v.ID,
					Status:      models.StatusOpen,
				})
			}
		}
	}
	return findings, nil
}

func mapOSVSeverity(sevs []struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}) models.Severity {
	for _, s := range sevs {
		n, err := strconv.ParseFloat(strings.TrimSpace(s.Score), 64)
		if err != nil {
			continue
		}
		switch {
		case n >= 9:
			return models.SeverityCritical
		case n >= 7:
			return models.SeverityHigh
		case n >= 4:
			return models.SeverityMedium
		default:
			return models.SeverityLow
		}
	}
	return models.SeverityHigh
}
