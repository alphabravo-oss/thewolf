package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// KubeLinterPlugin runs KubeLinter for Kubernetes manifest linting.
type KubeLinterPlugin struct{}

func init() {
	plugin.Register(&KubeLinterPlugin{})
}

func (p *KubeLinterPlugin) Name() string                 { return "kube-linter" }
func (p *KubeLinterPlugin) Category() models.Category    { return models.CategoryInfra }
func (p *KubeLinterPlugin) Languages() []models.Language { return nil }

func (p *KubeLinterPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *KubeLinterPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"kube-linter", "lint", "--format", "json", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		plugin.Skipf(opts.OnOutput, "kube-linter", "no Kubernetes manifests found. Add YAML files with K8s resources to enable linting.")
		return nil, nil
	}
	if len(bytes.TrimSpace(out)) == 0 {
		plugin.Skipf(opts.OnOutput, "kube-linter", "no Kubernetes manifests found. Add YAML files with K8s resources to enable linting.")
		return nil, nil
	}

	findings, perr := parseKubeLinterOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type kubeLinterOutput struct {
	Reports []kubeLinterReport `json:"Reports"`
}

type kubeLinterReport struct {
	Check       string `json:"Check"`
	Description string `json:"Description"`
	Remediation string `json:"Remediation"`
	Diagnostic  struct {
		Message string `json:"Message"`
	} `json:"Diagnostic"`
	Object struct {
		Metadata struct {
			FilePath string `json:"FilePath"`
		} `json:"K8sObject"`
	} `json:"Object"`
}

func parseKubeLinterOutput(data []byte) ([]models.Finding, error) {
	var output kubeLinterOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse kube-linter output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Reports))
	for _, r := range output.Reports {
		findings = append(findings, models.Finding{
			ToolName:    "kube-linter",
			Category:    models.CategoryInfra,
			Severity:    models.SeverityMedium,
			Title:       r.Check,
			Description: fmt.Sprintf("%s\n\nRemediation: %s", r.Diagnostic.Message, r.Remediation),
			FilePath:    r.Object.Metadata.FilePath,
			RuleID:      r.Check,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
