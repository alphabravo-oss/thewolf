package infra

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

// ConftestPlugin runs Open Policy Agent's conftest against the repo's
// YAML/JSON/HCL/Dockerfile configurations using policies discovered in the
// repo at:
//
//	policy/
//	tests/policy/
//	.conftest/
//
// If no policies are present, the plugin skips with a clear message.
// Conftest writes structured JSON to stdout; we parse each result.
type ConftestPlugin struct{}

func init() { plugin.Register(&ConftestPlugin{}) }

func (p *ConftestPlugin) Name() string                 { return "conftest" }
func (p *ConftestPlugin) Category() models.Category    { return models.CategoryInfra }
func (p *ConftestPlugin) Languages() []models.Language { return nil }

func (p *ConftestPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ConftestPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	policyDir := findConftestPolicyDir(opts.RepoPath)
	if policyDir == "" {
		plugin.Skipf(opts.OnOutput, "conftest",
			"no Rego policy directory found (looked for policy/, tests/policy/, .conftest/). "+
				"Add Rego policies to enable policy-as-code testing.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	relPolicy := strings.TrimPrefix(policyDir, opts.RepoPath)
	relPolicy = "/scan" + relPolicy

	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath},
		"conftest", "test", "/scan", "--policy", relPolicy, "--output", "json", "--no-color")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("conftest", err)
	}

	findings, perr := parseConftestOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

func findConftestPolicyDir(repo string) string {
	for _, c := range []string{"policy", "tests/policy", ".conftest"} {
		p := filepath.Join(repo, c)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

type conftestResult struct {
	Filename   string            `json:"filename"`
	Namespace  string            `json:"namespace"`
	Successes  int               `json:"successes"`
	Failures   []conftestMessage `json:"failures"`
	Warnings   []conftestMessage `json:"warnings"`
	Exceptions []conftestMessage `json:"exceptions"`
}

type conftestMessage struct {
	Msg      string `json:"msg"`
	Metadata struct {
		Details map[string]interface{} `json:"details"`
	} `json:"metadata"`
}

func parseConftestOutput(data []byte) ([]models.Finding, error) {
	var results []conftestResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("conftest: parse: %w", err)
	}
	var findings []models.Finding
	for _, r := range results {
		for _, f := range r.Failures {
			findings = append(findings, conftestFinding(r, f, models.SeverityHigh, "failure"))
		}
		for _, w := range r.Warnings {
			findings = append(findings, conftestFinding(r, w, models.SeverityMedium, "warning"))
		}
		for _, e := range r.Exceptions {
			findings = append(findings, conftestFinding(r, e, models.SeverityLow, "exception"))
		}
	}
	return findings, nil
}

func conftestFinding(r conftestResult, m conftestMessage, sev models.Severity, kind string) models.Finding {
	return models.Finding{
		ToolName:    "conftest",
		Category:    models.CategoryInfra,
		Severity:    sev,
		Title:       fmt.Sprintf("[%s/%s] %s", r.Namespace, kind, truncate(m.Msg, 60)),
		Description: m.Msg,
		FilePath:    r.Filename,
		RuleID:      r.Namespace,
		Status:      models.StatusOpen,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
