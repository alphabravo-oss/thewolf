package security

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
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

	plugin.SaveRaw(opts, out, "json")
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
	ID               string        `json:"id"`
	Summary          string        `json:"summary"`
	Details          string        `json:"details"`
	Severity         []plugin.CVSS `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Severity []plugin.CVSS `json:"severity"`
	} `json:"affected"`
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
				vectors := v.Severity
				for _, affected := range v.Affected {
					if affected.Package.Name == pkg.Package.Name && affected.Package.Ecosystem == pkg.Package.Ecosystem && len(affected.Severity) > 0 {
						vectors = affected.Severity
						break
					}
				}
				severity, provenance := plugin.ResolveSeverity(v.DatabaseSpecific.Severity, vectors, models.SeverityHigh)
				findings = append(findings, models.Finding{
					ToolName:         "osv-scanner",
					Category:         models.CategorySCA,
					Severity:         severity,
					Title:            fmt.Sprintf("%s: %s", v.ID, v.Summary),
					Description:      fmt.Sprintf("Package: %s@%s (%s)\n\n%s\n\n%s", pkg.Package.Name, pkg.Package.Version, pkg.Package.Ecosystem, v.Details, provenance),
					FilePath:         r.Source.Path,
					RuleID:           v.ID,
					AdvisoryID:       identity.CanonicalAdvisory(v.ID, v.Aliases...),
					PackageName:      pkg.Package.Name,
					PackageVersion:   pkg.Package.Version,
					PackageEcosystem: pkg.Package.Ecosystem,
					Status:           models.StatusOpen,
				})
			}
		}
	}
	return findings, nil
}
