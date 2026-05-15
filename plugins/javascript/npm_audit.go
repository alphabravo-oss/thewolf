package javascript

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
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

func (p *NPMAuditPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *NPMAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	jsDir := plugin.FindFile(opts.RepoPath, "package-lock.json")
	if jsDir == "" {
		jsDir = plugin.FindFile(opts.RepoPath, "package.json")
	}
	if jsDir == "" {
		plugin.Skipf(opts.OnOutput, "npm-audit", "no package.json found in project or immediate subdirectories.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, jsDir),
		},
		"npm", "audit", "--json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("npm-audit", err)
	}

	// Compute the repo-relative package.json path so each finding has a
	// meaningful location. npm-audit itself reports against packages,
	// not files; without this the UI shows blank file/line for every
	// SCA finding. For a monorepo with multiple package.json files, we
	// use the one whose tree this audit ran in (the same jsDir we
	// chose above).
	relPkg := "package.json"
	if rel, rerr := filepath.Rel(opts.RepoPath, jsDir); rerr == nil && rel != "" && rel != "." {
		relPkg = filepath.ToSlash(filepath.Join(rel, "package.json"))
	}
	// container.NormalizePath strips the in-container /scan/ prefix; we
	// already built relPkg as repo-relative so just trim any leading
	// slash for consistency.
	relPkg = strings.TrimPrefix(relPkg, "/")

	findings, perr := parseNPMAuditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		// npm-audit emits no file_path; the package.json that declared
		// the vulnerable dep is the right anchor for the UI's file
		// column + the click-through to source.
		if findings[i].FilePath == "" {
			findings[i].FilePath = relPkg
		} else {
			findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
		}
	}
	return findings, nil
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
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
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
