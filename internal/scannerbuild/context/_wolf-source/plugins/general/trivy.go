package general

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// TrivyPlugin runs Trivy vulnerability scanning.
type TrivyPlugin struct{}

func init() {
	plugin.Register(&TrivyPlugin{})
}

func (p *TrivyPlugin) Name() string                 { return "trivy" }
func (p *TrivyPlugin) Category() models.Category    { return models.CategorySCA }
func (p *TrivyPlugin) Languages() []models.Language { return nil }

func (p *TrivyPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *TrivyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cache := "/var/lib/wolf-db/trivy"
	if cfg != nil && cfg.ExtraEnv["TRIVY_CACHE_DIR"] != "" {
		cache = cfg.ExtraEnv["TRIVY_CACHE_DIR"]
	}
	args := []string{"fs", "--format", "json", "--scanners", "vuln", "--cache-dir", cache}
	if container.ResolveNetwork(cfg, "trivy") == "none" {
		args = append(args, "--skip-db-update", "--skip-java-db-update", "--offline-scan")
	}
	args = append(args, "/scan")
	cmd := container.CommandContext(ctx, cfg, container.Options{
		RepoDir: opts.RepoPath, ExtraEnv: map[string]string{"HOME": "/tmp", "TRIVY_CACHE_DIR": cache},
	}, "trivy", args...)
	out, err := cmd.Output()
	if err != nil {
		err = plugin.WrapExecError("trivy", err)
		if len(out) == 0 {
			return nil, err
		}
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseTrivyOutput(out)
	if perr != nil {
		return nil, errors.Join(err, perr)
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, err
}

type trivyOutput struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Type            string               `json:"Type"`
	Target          string               `json:"Target"`
	Vulnerabilities []trivyVulnerability `json:"Vulnerabilities"`
}

type trivyVulnerability struct {
	PkgIdentifier struct {
		PURL string `json:"PURL"`
	} `json:"PkgIdentifier"`
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
				ToolName:         "trivy",
				Category:         models.CategorySCA,
				Severity:         mapTrivySeverity(vuln.Severity),
				Title:            fmt.Sprintf("%s in %s@%s", vuln.VulnerabilityID, vuln.PkgName, vuln.InstalledVersion),
				Description:      desc,
				FilePath:         result.Target,
				RuleID:           vuln.VulnerabilityID,
				AdvisoryID:       vuln.VulnerabilityID,
				PackageName:      vuln.PkgName,
				PackageVersion:   vuln.InstalledVersion,
				PackageEcosystem: result.Type,
				PackageURL:       vuln.PkgIdentifier.PURL,
				Status:           models.StatusOpen,
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
