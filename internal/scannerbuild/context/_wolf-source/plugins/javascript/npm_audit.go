package javascript

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
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

func (p *NPMAuditPlugin) Name() string              { return "npm-audit" }
func (p *NPMAuditPlugin) Category() models.Category { return models.CategorySCA }
func (p *NPMAuditPlugin) Languages() []models.Language {
	return []models.Language{models.LangJavaScript, models.LangTypeScript}
}

func (p *NPMAuditPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *NPMAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	manifest := "package-lock.json"
	jsDir := plugin.FindFile(opts.RepoPath, manifest)
	if jsDir == "" {
		manifest = "package.json"
		jsDir = plugin.FindFile(opts.RepoPath, manifest)
	}
	if jsDir == "" {
		return nil, p.Applicability(opts.RepoPath, "")
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

	// Compute the repo-relative audited manifest path so each finding has a
	// meaningful location. npm-audit itself reports against packages,
	// not files; without this the UI shows blank file/line for every
	// SCA finding. For a monorepo with multiple package.json files, we
	// use the lockfile (or manifest fallback) whose tree this audit ran in (the same jsDir we
	// chose above).
	relPkg := manifest
	if rel, rerr := filepath.Rel(opts.RepoPath, jsDir); rerr == nil && rel != "" && rel != "." {
		relPkg = filepath.ToSlash(filepath.Join(rel, manifest))
	}
	// container.NormalizePath strips the in-container /scan/ prefix; we
	// already built relPkg as repo-relative so just trim any leading
	// slash for consistency.
	relPkg = strings.TrimPrefix(relPkg, "/")

	plugin.SaveRaw(opts, out, "json")
	findings, perr := parseNPMAuditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		// npm-audit emits no file_path; the audited lockfile/manifest is the right anchor for the UI's file
		// column + the click-through to source.
		findings[i].FilePath = relPkg
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
	Severity string `json:"severity"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	CVE      string `json:"cve"`
	Range    string `json:"range"`
	Source   int    `json:"source"`
}

func parseNPMAuditOutput(data []byte) ([]models.Finding, error) {
	var output npmAuditOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse npm audit output: %w", err)
	}

	var findings []models.Finding
	for pkgName, vuln := range output.Vulnerabilities {
		pkgName = strings.TrimSpace(pkgName)
		if pkgName == "" {
			return nil, fmt.Errorf("npm audit vulnerability has no package name")
		}
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

			title := strings.TrimSpace(via.Title)
			if title == "" {
				title = pkgName
			}
			ruleID := strings.TrimSpace(via.CVE)
			if ruleID == "" {
				ruleID = strings.TrimSpace(via.URL)
			}
			if ruleID == "" && via.Source > 0 {
				ruleID = "npm:" + strconv.Itoa(via.Source)
			}
			if ruleID == "" {
				ruleID = title
			}

			rating := via.Severity
			if strings.TrimSpace(rating) == "" {
				rating = vuln.Severity
			}
			severity, provenance := plugin.ResolveSeverity(rating, nil, models.SeverityHigh)
			findings = append(findings, models.Finding{
				ToolName:         "npm-audit",
				Category:         models.CategorySCA,
				Severity:         severity,
				Title:            title,
				Description:      fmt.Sprintf("Vulnerable package: %s (range: %s, cve: %s, url: %s)\n\n%s", pkgName, via.Range, via.CVE, via.URL, provenance),
				RuleID:           ruleID,
				AdvisoryID:       ruleID,
				PackageName:      pkgName,
				PackageEcosystem: "npm",
				FilePath:         "package-lock.json",
				Status:           models.StatusOpen,
			})
		}
	}
	return findings, nil
}

// Applicability keeps absent inputs out of selection and coverage.
func (p *NPMAuditPlugin) Applicability(repoPath, _ string) error {
	return plugin.Absent(plugin.FindFile(repoPath, "package-lock.json") == "" && plugin.FindFile(repoPath, "package.json") == "", "no package.json found in project or immediate subdirectories.")
}
