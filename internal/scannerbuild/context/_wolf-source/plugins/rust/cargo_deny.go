package rust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// CargoDenyPlugin runs cargo-deny's advisory check against Cargo.lock.
// License/ban/source policy is applied only when the repo ships deny.toml.
type CargoDenyPlugin struct{}

func init() { plugin.Register(&CargoDenyPlugin{}) }

func (p *CargoDenyPlugin) Name() string              { return "cargo-deny" }
func (p *CargoDenyPlugin) Category() models.Category { return models.CategorySCA }
func (p *CargoDenyPlugin) Languages() []models.Language {
	return []models.Language{models.LangRust}
}

func (p *CargoDenyPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *CargoDenyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	lockDir := plugin.FindFile(opts.RepoPath, "Cargo.lock")
	if lockDir == "" {
		plugin.Skipf(opts.OnOutput, "cargo-deny", "no Cargo.lock found. Commit a lockfile to enable Rust policy scanning.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	home := "/tmp"
	if cfg.DBVolume != "" {
		home = "/var/lib/wolf-db/cargo-deny"
	}
	cmdArgs := []string{"--format", "json", "check", "advisories"}
	if plugin.HasFile(lockDir, "deny.toml") {
		cmdArgs = []string{"--format", "json", "check"}
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
		"cargo-deny", cmdArgs...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("cargo-deny", err)
	}
	findings, perr := parseCargoDenyOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type cargoDenyReport struct {
	Diagnostics []cargoDenyDiag `json:"diagnostics"`
}

type cargoDenyDiag struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     any    `json:"code"`
	Labels   []struct {
		Message string `json:"message"`
		Span    string `json:"span"`
	} `json:"labels"`
}

func parseCargoDenyOutput(data []byte) ([]models.Finding, error) {
	raw := plugin.ExtractJSON(data)
	var report cargoDenyReport
	if err := json.Unmarshal(raw, &report); err != nil {
		// cargo-deny sometimes emits a bare diagnostic array.
		var diags []cargoDenyDiag
		if err2 := json.Unmarshal(raw, &diags); err2 != nil {
			return nil, fmt.Errorf("cargo-deny: parse: %w", err)
		}
		report.Diagnostics = diags
	}
	var findings []models.Finding
	for _, diag := range report.Diagnostics {
		if strings.EqualFold(diag.Type, "summary") {
			continue
		}
		code := cargoDenyCode(diag.Code)
		if code == "" && diag.Message == "" {
			continue
		}
		findings = append(findings, models.Finding{
			ToolName:    "cargo-deny",
			Category:    models.CategorySCA,
			Severity:    mapCargoDenySeverity(diag.Severity),
			Title:       firstNonEmpty(code, "cargo-deny"),
			Description: strings.TrimSpace(diag.Message),
			FilePath:    "Cargo.lock",
			RuleID:      code,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func cargoDenyCode(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["code"].(string); ok {
			return s
		}
	}
	return ""
}

func mapCargoDenySeverity(level string) models.Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note", "help":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
