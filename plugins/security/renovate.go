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
				"RENOVATE_DRY_RUN":               "full",
				"RENOVATE_AUTODISCOVER":          "false",
				"RENOVATE_PLATFORM":              "local",
				"RENOVATE_BASE_DIR":              "/tmp/renovate",
				"RENOVATE_CACHE_DIR":             "/tmp/renovate-cache",
				"LOG_FORMAT":                     "json",
				// Debug level emits the "packageFiles with updates"
				// log line that has every detected upgrade. Info
				// level only prints summary counts so we'd miss the
				// per-dep entries entirely.
				"LOG_LEVEL":                      "debug",
				"RENOVATE_REQUIRE_CONFIG":        "optional",
				"RENOVATE_BINARY_SOURCE":         "install",
				"RENOVATE_PERSIST_REPO_DATA":     "false",
				"RENOVATE_OPTIMIZE_FOR_DISABLED": "false",
				// On RENOVATE_PLATFORM=local, the default onboarding
				// workflow crashes with "Cannot read properties of
				// undefined" inside getBranchCommit. Skip onboarding
				// + the dashboard-issue creator since neither makes
				// sense in detect-only mode anyway.
				"RENOVATE_ONBOARDING":            "false",
				"RENOVATE_DEPENDENCY_DASHBOARD":  "false",
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

// renovateLogLine is the shape of one renovate log entry. The interesting
// one is "packageFiles with updates" — emitted at debug level — whose
// `config` field carries the full per-manager dependency tree.
type renovateLogLine struct {
	Level  int                            `json:"level"`
	Msg    string                         `json:"msg"`
	Config map[string][]renovateManagerPF `json:"config,omitempty"`
}

// renovateManagerPF is the per-manager, per-packageFile entry. Each one
// holds a slice of deps; each dep may have zero or more proposed updates.
type renovateManagerPF struct {
	PackageFile string        `json:"packageFile"`
	Deps        []renovateDep `json:"deps"`
}

type renovateDep struct {
	DepName              string             `json:"depName"`
	CurrentValue         string             `json:"currentValue"`
	CurrentVersion       string             `json:"currentVersion"`
	DepType              string             `json:"depType"`
	Updates              []renovateUpdate   `json:"updates"`
	Vulnerabilities      []renovateVulnInfo `json:"vulnerabilities,omitempty"`
	IsVulnerabilityAlert bool               `json:"isVulnerabilityAlert,omitempty"`
}

type renovateUpdate struct {
	NewValue                string `json:"newValue"`
	NewVersion              string `json:"newVersion"`
	UpdateType              string `json:"updateType"`
	NewVersionAgeInDays     int    `json:"newVersionAgeInDays"`
	IsVulnerabilityAlert    bool   `json:"isVulnerabilityAlert,omitempty"`
	VulnerabilityFixVersion string `json:"vulnerabilityFixVersion,omitempty"`
}

type renovateVulnInfo struct {
	PackageName string `json:"packageName"`
	FixedIn     string `json:"fixedIn"`
}

// parseRenovateOutput walks the JSON log stream looking for the
// "packageFiles with updates" message, then iterates every (manager,
// packageFile, dep, update) combination and emits one Finding per
// proposed upgrade.
//
// Renovate's --dry-run=full produces one such message per repository
// scan. Other log lines (debug, info noise) are forwarded to OnOutput
// for live tailing when set, but never produce findings on their own.
func parseRenovateOutput(data []byte, onOutput func(string)) ([]models.Finding, error) {
	seen := make(map[string]struct{})
	var findings []models.Finding

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Renovate "packageFiles with updates" lines can be very large
	// (hundreds of deps × full registry metadata). 16 MB upper bound is
	// safe; even monstrous monorepos fit.
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var line renovateLogLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		// Forward non-update messages to the live log stream.
		if line.Config == nil {
			if onOutput != nil && line.Msg != "" {
				onOutput(line.Msg)
			}
			continue
		}
		for manager, files := range line.Config {
			for _, pf := range files {
				for _, dep := range pf.Deps {
					for _, upd := range dep.Updates {
						if upd.UpdateType == "" {
							continue
						}
						key := manager + "|" + pf.PackageFile + "|" + dep.DepName + "|" + dep.CurrentValue + "→" + upd.NewValue
						if _, dup := seen[key]; dup {
							continue
						}
						seen[key] = struct{}{}
						findings = append(findings, renovateFinding(manager, pf.PackageFile, dep, upd))
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan renovate output: %w", err)
	}
	return findings, nil
}

func renovateFinding(manager, packageFile string, dep renovateDep, upd renovateUpdate) models.Finding {
	isVuln := dep.IsVulnerabilityAlert || upd.IsVulnerabilityAlert || len(dep.Vulnerabilities) > 0
	severity := renovateSeverity(upd.UpdateType, isVuln)
	current := nzString(dep.CurrentValue, dep.CurrentVersion)
	newer := nzString(upd.NewValue, upd.NewVersion)
	title := fmt.Sprintf("%s: %s %s → %s (%s)",
		nzString(manager, "dep"),
		dep.DepName, current, newer, upd.UpdateType)
	desc := fmt.Sprintf("Renovate detected an available %s update for %s in %s: %s → %s.",
		upd.UpdateType, dep.DepName, nzString(packageFile, "<manifest>"),
		current, newer)
	if upd.NewVersionAgeInDays > 0 {
		desc += fmt.Sprintf(" Target is %d days old.", upd.NewVersionAgeInDays)
	}
	if isVuln {
		desc += " Known vulnerability in current version."
		if upd.VulnerabilityFixVersion != "" {
			desc += " Fix in " + upd.VulnerabilityFixVersion + "."
		}
	}
	return models.Finding{
		ToolName:    "renovate",
		Category:    models.CategorySCA,
		Severity:    severity,
		Title:       title,
		Description: desc,
		FilePath:    packageFile,
		RuleID:      "renovate-" + strings.ToLower(upd.UpdateType),
		Status:      models.StatusOpen,
	}
}

// renovateSeverity assigns wolf severity per update gap, with a known
// vulnerability bumping the result up to High regardless of gap.
//
//	patch  → info
//	minor  → low
//	major  → medium
//	+ vuln → high
func renovateSeverity(updateType string, isVuln bool) models.Severity {
	if isVuln {
		return models.SeverityHigh
	}
	switch strings.ToLower(updateType) {
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
