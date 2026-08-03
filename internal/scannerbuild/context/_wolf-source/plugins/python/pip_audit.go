package python

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// PipAuditPlugin runs pip-audit dependency vulnerability scanner for Python.
type PipAuditPlugin struct{}

func init() {
	plugin.Register(&PipAuditPlugin{})
}

func (p *PipAuditPlugin) Name() string                 { return "pip-audit" }
func (p *PipAuditPlugin) Category() models.Category    { return models.CategorySCA }
func (p *PipAuditPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *PipAuditPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *PipAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Auto-detect requirements files
	reqFiles := []string{"requirements.txt", "requirements/base.txt", "requirements/prod.txt", "requirements-dev.txt"}
	var reqFile string
	for _, f := range reqFiles {
		if _, err := os.Stat(filepath.Join(opts.RepoPath, f)); err == nil {
			reqFile = f
			break
		}
	}
	if reqFile == "" {
		plugin.Skipf(opts.OnOutput, "pip-audit", "no requirements file found (checked requirements.txt, requirements/base.txt, requirements/prod.txt, requirements-dev.txt). Add a requirements file or use pip freeze > requirements.txt to enable dependency auditing.")
		return nil, nil
	}

	// pip-audit needs the workdir set to /scan so its -r is resolved relative
	// to the repo root inside the container.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath, WorkDir: "/scan"},
		"pip-audit", "-r", reqFile, "-f", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("pip-audit", err)
	}

	findings, perr := parsePipAuditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type pipAuditOutput struct {
	Dependencies []pipAuditDependency `json:"dependencies"`
}

type pipAuditDependency struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Vulns   []pipAuditVuln `json:"vulns"`
}

type pipAuditVuln struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	FixVersions []string `json:"fix_versions"`
	Aliases     []string `json:"aliases"`
}

func parsePipAuditOutput(data []byte) ([]models.Finding, error) {
	var output pipAuditOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse pip-audit output: %w", err)
	}

	var findings []models.Finding
	for _, dep := range output.Dependencies {
		for _, vuln := range dep.Vulns {
			cveID := extractCVEFromPipAudit(vuln)
			fixInfo := ""
			if len(vuln.FixVersions) > 0 {
				fixInfo = fmt.Sprintf(" Fix available in version(s): %s.", strings.Join(vuln.FixVersions, ", "))
			}

			findings = append(findings, models.Finding{
				ToolName:    "pip-audit",
				Category:    models.CategorySCA,
				Severity:    models.SeverityMedium,
				Title:       fmt.Sprintf("Vulnerability %s in %s@%s", vuln.ID, dep.Name, dep.Version),
				Description: fmt.Sprintf("%s%s", vuln.Description, fixInfo),
				FilePath:    "requirements.txt",
				RuleID:      vuln.ID,
				CWEID:       cveID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func extractCVEFromPipAudit(vuln pipAuditVuln) string {
	if strings.HasPrefix(vuln.ID, "CVE-") {
		return vuln.ID
	}
	for _, alias := range vuln.Aliases {
		if strings.HasPrefix(alias, "CVE-") {
			return alias
		}
	}
	return ""
}
