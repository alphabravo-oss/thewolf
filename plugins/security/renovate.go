package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// RenovatePlugin runs Mend's Renovate in dry-run / detect-only mode against
// the repository and emits findings for every outdated dependency, prioritized
// by how far behind the dep is and whether renovate flags it as a known
// vulnerability.
//
// Wolf NEVER allows renovate to open PRs — it's strictly a detector here.
// We set RENOVATE_DRY_RUN=full and RENOVATE_AUTODISCOVER=false so the binary
// inspects the repo, logs the upgrades it WOULD make, and exits.
//
// Coverage that overlaps with trivy/grype/osv-scanner: npm, pip, gem, composer,
// cargo, go modules. Coverage that's uniquely renovate: Helm charts, GitHub
// Actions, Dockerfile base images, Terraform modules, pre-commit hooks,
// GitLab CI, Bitbucket pipelines, Gradle / Maven / sbt.
type RenovatePlugin struct{}

func init() { plugin.Register(&RenovatePlugin{}) }

func (p *RenovatePlugin) Name() string                 { return "renovate" }
func (p *RenovatePlugin) Category() models.Category    { return models.CategorySCA }
func (p *RenovatePlugin) Languages() []models.Language { return nil }

func (p *RenovatePlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *RenovatePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// Renovate needs HOME, a writable cache, and a writable repo workspace.
	// The wolf shim mounts /scan read-only — renovate can detect what's there
	// without writing.
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir: opts.RepoPath,
			ExtraEnv: map[string]string{
				"RENOVATE_DRY_RUN":            "full",
				"RENOVATE_AUTODISCOVER":       "false",
				"RENOVATE_PLATFORM":           "local",
				"RENOVATE_BASE_DIR":           "/tmp/renovate",
				"RENOVATE_CACHE_DIR":          "/tmp/renovate-cache",
				"LOG_FORMAT":                  "json",
				"LOG_LEVEL":                   "info",
				"RENOVATE_REQUIRE_CONFIG":     "optional",
				"RENOVATE_BINARY_SOURCE":      "install",
				"RENOVATE_PERSIST_REPO_DATA":  "false",
				"RENOVATE_OPTIMIZE_FOR_DISABLED": "false",
			},
		},
		"renovate")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("renovate", err)
	}

	findings, perr := parseRenovateOutput(out, opts.OnOutput)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// renovateLogLine is one line of LOG_FORMAT=json output. We only care about
// the upgrade-bearing entries.
type renovateLogLine struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	DepName string `json:"depName,omitempty"`
	// PackageFile is set on dependency-related log lines; for finding-emission
	// we use it as the FilePath so the user knows WHICH manifest holds the dep.
	PackageFile  string `json:"packageFile,omitempty"`
	Manager      string `json:"manager,omitempty"`
	CurrentValue string `json:"currentValue,omitempty"`
	NewValue     string `json:"newValue,omitempty"`
	UpdateType   string `json:"updateType,omitempty"`
	// IsVulnerabilityAlert marks deps with known CVEs. When set, severity bumps to high.
	IsVulnerabilityAlert bool `json:"isVulnerabilityAlert,omitempty"`
	// VulnerabilityFixVersion, when present alongside IsVulnerabilityAlert, indicates the
	// release that closes the advisory.
	VulnerabilityFixVersion string `json:"vulnerabilityFixVersion,omitempty"`
}

// parseRenovateOutput consumes newline-delimited JSON log lines from renovate
// and produces one finding per detected upgrade. Non-upgrade log lines (config
// validation, platform checks, etc.) are forwarded to opts.OnOutput for live
// streaming when provided.
//
// Renovate's JSON log shape varies across versions; we treat any line with a
// depName + (currentValue,newValue,updateType) as an upgrade entry. Lines
// without a depName are info-level streaming output.
func parseRenovateOutput(data []byte, onOutput func(string)) ([]models.Finding, error) {
	// Dedup by (manager, packageFile, depName) — renovate sometimes logs the
	// same upgrade under multiple log lines (branch, PR, branch summary, …).
	seen := make(map[string]struct{})
	var findings []models.Finding

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Renovate log lines can be very wide; bump the scanner buffer.
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var line renovateLogLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		if line.DepName == "" || line.UpdateType == "" {
			if onOutput != nil && line.Msg != "" {
				onOutput(line.Msg)
			}
			continue
		}
		key := line.Manager + "|" + line.PackageFile + "|" + line.DepName + "|" + line.CurrentValue + "→" + line.NewValue
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		findings = append(findings, renovateFinding(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan renovate output: %w", err)
	}
	return findings, nil
}

func renovateFinding(l renovateLogLine) models.Finding {
	severity := renovateSeverity(l)
	title := fmt.Sprintf("%s: %s %s → %s (%s)",
		nzString(l.Manager, "dep"),
		l.DepName, l.CurrentValue, l.NewValue, l.UpdateType)
	desc := fmt.Sprintf("Renovate detected an available %s update for %s in %s: %s → %s.",
		l.UpdateType, l.DepName, nzString(l.PackageFile, "<manifest>"),
		l.CurrentValue, l.NewValue)
	if l.IsVulnerabilityAlert {
		desc += " Known vulnerability in current version."
		if l.VulnerabilityFixVersion != "" {
			desc += " Fix available in " + l.VulnerabilityFixVersion + "."
		}
	}
	return models.Finding{
		ToolName:    "renovate",
		Category:    models.CategorySCA,
		Severity:    severity,
		Title:       title,
		Description: desc,
		FilePath:    l.PackageFile,
		RuleID:      "renovate-" + strings.ToLower(l.UpdateType),
		Status:      models.StatusOpen,
	}
}

// renovateSeverity assigns wolf severity per update gap, with a vulnerability
// flag bumping to High regardless of gap.
//
//	patch      → info
//	minor      → low
//	major      → medium
//	+ vuln     → high
//
// Other update types (pin, digest, replacement, lockFileMaintenance, rollback,
// bump, pinDigest) are noise-level info — wolf surfaces them but at the lowest
// priority.
func renovateSeverity(l renovateLogLine) models.Severity {
	if l.IsVulnerabilityAlert {
		return models.SeverityHigh
	}
	switch strings.ToLower(l.UpdateType) {
	case "patch":
		return models.SeverityInfo
	case "minor":
		return models.SeverityLow
	case "major":
		return models.SeverityMedium
	default:
		return models.SeverityInfo
	}
}

func nzString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
