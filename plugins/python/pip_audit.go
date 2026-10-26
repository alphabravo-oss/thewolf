package python

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
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

var pipRequirementFiles = []string{"requirements.txt", "requirements/base.txt", "requirements/prod.txt", "requirements-dev.txt"}

func (p *PipAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Auto-detect requirements files
	var reqFile string
	for _, f := range pipRequirementFiles {
		if _, err := os.Stat(filepath.Join(opts.RepoPath, f)); err == nil {
			reqFile = f
			break
		}
	}
	if reqFile == "" {
		return nil, p.Applicability(opts.RepoPath, "")
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

	plugin.SaveRaw(opts, out, "json")
	findings, perr := parsePipAuditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = filepath.ToSlash(reqFile)
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
	Severity    string        `json:"severity"`
	CVSS        []plugin.CVSS `json:"cvss"`
	ID          string        `json:"id"`
	Description string        `json:"description"`
	FixVersions []string      `json:"fix_versions"`
	Aliases     []string      `json:"aliases"`
}

func parsePipAuditOutput(data []byte) ([]models.Finding, error) {
	var output pipAuditOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse pip-audit output: %w", err)
	}

	var findings []models.Finding
	for _, dep := range output.Dependencies {
		for _, vuln := range dep.Vulns {
			severity, provenance := plugin.ResolveSeverity(vuln.Severity, vuln.CVSS, models.SeverityMedium)
			fixInfo := ""
			if len(vuln.FixVersions) > 0 {
				fixInfo = fmt.Sprintf(" Fix available in version(s): %s.", strings.Join(vuln.FixVersions, ", "))
			}

			findings = append(findings, models.Finding{
				ToolName:         "pip-audit",
				Category:         models.CategorySCA,
				Severity:         severity,
				Title:            fmt.Sprintf("Vulnerability %s in %s@%s", vuln.ID, dep.Name, dep.Version),
				Description:      fmt.Sprintf("%s%s\n\n%s", vuln.Description, fixInfo, provenance),
				FilePath:         "requirements.txt",
				RuleID:           vuln.ID,
				AdvisoryID:       identity.CanonicalAdvisory(vuln.ID, vuln.Aliases...),
				PackageName:      dep.Name,
				PackageVersion:   dep.Version,
				PackageEcosystem: "pypi",
				Status:           models.StatusOpen,
			})
		}
	}
	return findings, nil
}

// Applicability keeps absent inputs out of selection and coverage.
func (p *PipAuditPlugin) Applicability(repoPath, _ string) error {
	return plugin.Absent(!slices.ContainsFunc(pipRequirementFiles, func(f string) bool { return plugin.HasFile(repoPath, f) }), "no requirements file found (checked requirements.txt, requirements/base.txt, requirements/prod.txt, requirements-dev.txt). Add a requirements file or use pip freeze > requirements.txt to enable dependency auditing.")
}
