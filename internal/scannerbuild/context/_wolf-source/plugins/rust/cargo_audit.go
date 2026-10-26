package rust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// CargoAuditPlugin audits Cargo.lock against the RustSec advisory database.
type CargoAuditPlugin struct{}

func init() { plugin.Register(&CargoAuditPlugin{}) }

func (p *CargoAuditPlugin) Name() string              { return "cargo-audit" }
func (p *CargoAuditPlugin) Category() models.Category { return models.CategorySCA }
func (p *CargoAuditPlugin) Languages() []models.Language {
	return []models.Language{models.LangRust}
}

func (p *CargoAuditPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *CargoAuditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	lockDir := plugin.FindFile(opts.RepoPath, "Cargo.lock")
	if lockDir == "" {
		return nil, p.Applicability(opts.RepoPath, "")
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	home := "/tmp"
	if cfg.DBVolume != "" {
		home = "/var/lib/wolf-db/cargo-audit"
	}
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, lockDir),
			ExtraEnv: map[string]string{
				"HOME":       home,
				"CARGO_HOME": home,
			},
		},
		"cargo-audit", "audit", "--json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("cargo-audit", err)
	}
	findings, perr := parseCargoAuditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type cargoAuditOutput struct {
	Vulnerabilities struct {
		List []cargoAuditVuln `json:"list"`
	} `json:"vulnerabilities"`
	Warnings json.RawMessage `json:"warnings"`
}

type cargoAuditVuln struct {
	Advisory struct {
		ID          string   `json:"id"`
		Package     string   `json:"package"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		URL         string   `json:"url"`
		Aliases     []string `json:"aliases"`
	} `json:"advisory"`
	Package struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"package"`
}

func parseCargoAuditOutput(data []byte) ([]models.Finding, error) {
	var doc cargoAuditOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &doc); err != nil {
		return nil, fmt.Errorf("cargo-audit: parse: %w", err)
	}
	var findings []models.Finding
	for _, vuln := range doc.Vulnerabilities.List {
		pkg := vuln.Package.Name
		if pkg == "" {
			pkg = vuln.Advisory.Package
		}
		title := vuln.Advisory.Title
		if title == "" {
			title = vuln.Advisory.ID
		}
		desc := vuln.Advisory.Description
		if pkg != "" && vuln.Package.Version != "" {
			desc = fmt.Sprintf("%s %s: %s", pkg, vuln.Package.Version, desc)
		}
		if vuln.Advisory.URL != "" {
			desc = strings.TrimSpace(desc + "\n" + vuln.Advisory.URL)
		}
		for _, alias := range vuln.Advisory.Aliases {
			if strings.HasPrefix(alias, "CVE-") {
				desc = strings.TrimSpace(desc + "\n" + alias)
				break
			}
		}
		findings = append(findings, models.Finding{
			ToolName:         "cargo-audit",
			Category:         models.CategorySCA,
			Severity:         models.SeverityHigh,
			Title:            title,
			Description:      desc,
			FilePath:         "Cargo.lock",
			RuleID:           vuln.Advisory.ID,
			AdvisoryID:       identity.CanonicalAdvisory(vuln.Advisory.ID, vuln.Advisory.Aliases...),
			PackageName:      pkg,
			PackageVersion:   vuln.Package.Version,
			PackageEcosystem: "cargo",
			Status:           models.StatusOpen,
		})
	}
	return findings, nil
}

// Applicability keeps absent inputs out of selection and coverage.
func (p *CargoAuditPlugin) Applicability(repoPath, _ string) error {
	return plugin.Absent(plugin.FindFile(repoPath, "Cargo.lock") == "", "no Cargo.lock found. Commit a lockfile to enable Rust advisory scanning.")
}
