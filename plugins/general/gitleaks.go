package general

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// GitleaksPlugin runs Gitleaks secret detection.
type GitleaksPlugin struct{}

func init() {
	plugin.Register(&GitleaksPlugin{})
}

func (p *GitleaksPlugin) Name() string               { return "gitleaks" }
func (p *GitleaksPlugin) Category() models.Category   { return models.CategorySecrets }
func (p *GitleaksPlugin) Languages() []models.Language { return nil }

func (p *GitleaksPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *GitleaksPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Use stdout for the report instead of a host temp file.
	// gitleaks 8.x supports "-" as the report path to mean stdout.
	// The default gitleaks ruleset already covers the patterns we want;
	// exclude paths come from the gitleaks "allowlist" baked into a config
	// file at /etc/wolf-scanners/gitleaks.toml in the scanners image.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"gitleaks", "detect", "--source", "/scan",
		"--report-format", "json", "--report-path", "/dev/stdout",
		"--no-git",
		"--exit-code", "0") // suppress non-zero on findings
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		plugin.Infof(opts.OnOutput, "gitleaks", "scan completed, no secrets detected.")
		return nil, nil
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseGitleaksOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type gitleaksFinding struct {
	Description string `json:"Description"`
	File        string `json:"File"`
	StartLine   int    `json:"StartLine"`
	EndLine     int    `json:"EndLine"`
	Match       string `json:"Match"`
	RuleID      string `json:"RuleID"`
	Entropy     float64 `json:"Entropy"`
	Secret      string `json:"Secret"`
}

func parseGitleaksOutput(data []byte) ([]models.Finding, error) {
	var results []gitleaksFinding
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse gitleaks output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, models.Finding{
			ToolName:    "gitleaks",
			Category:    models.CategorySecrets,
			Severity:    models.SeverityHigh,
			Title:       fmt.Sprintf("Secret detected: %s", r.RuleID),
			Description: r.Description,
			FilePath:    r.File,
			LineStart:   r.StartLine,
			LineEnd:     r.EndLine,
			CodeSnippet: r.Match,
			RuleID:      r.RuleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
