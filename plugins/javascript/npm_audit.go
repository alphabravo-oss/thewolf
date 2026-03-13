package javascript

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// NPMAuditPlugin runs npm audit for dependency vulnerability scanning.
type NPMAuditPlugin struct{}

func init() {
	plugin.Register(&NPMAuditPlugin{})
}

func (p *NPMAuditPlugin) Name() string             { return "npm-audit" }
func (p *NPMAuditPlugin) Category() models.Category { return models.CategorySCA }
func (p *NPMAuditPlugin) Languages() []models.Language {
	return []models.Language{models.LangJavaScript, models.LangTypeScript}
}

func (p *NPMAuditPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("npm")
	return err == nil
}

func (p *NPMAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "npm", "audit", "--json")
	cmd.Dir = opts.RepoPath
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("npm audit execution failed: %w", err)
		}
	}

	return parseNPMAuditOutput(out)
}

type npmAuditOutput struct {
	Vulnerabilities map[string]npmVulnerability `json:"vulnerabilities"`
}

type npmVulnerability struct {
	Severity string        `json:"severity"`
	Via      []interface{} `json:"via"`
}

type npmViaEntry struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	CVE    string `json:"cve"`
	Range  string `json:"range"`
	Source int    `json:"source"`
}

func parseNPMAuditOutput(data []byte) ([]models.Finding, error) {
	var output npmAuditOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse npm audit output: %w", err)
	}

	var findings []models.Finding
	for pkgName, vuln := range output.Vulnerabilities {
		for _, raw := range vuln.Via {
			// via entries can be strings (transitive deps) — skip those.
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}

			var via npmViaEntry
			b, err := json.Marshal(m)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(b, &via); err != nil {
				continue
			}

			title := via.Title
			if title == "" {
				title = pkgName
			}

			findings = append(findings, models.Finding{
				ToolName:    "npm-audit",
				Category:    models.CategorySCA,
				Severity:    mapNPMAuditSeverity(vuln.Severity),
				Title:       title,
				Description: fmt.Sprintf("Vulnerable package: %s (range: %s, cve: %s, url: %s)", pkgName, via.Range, via.CVE, via.URL),
				CWEID:       via.CVE,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapNPMAuditSeverity(s string) models.Severity {
	switch s {
	case "critical":
		return models.SeverityCritical
	case "high":
		return models.SeverityHigh
	case "moderate":
		return models.SeverityMedium
	case "low":
		return models.SeverityLow
	case "info":
		return models.SeverityInfo
	default:
		return models.SeverityInfo
	}
}
