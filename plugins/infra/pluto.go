package infra

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// PlutoPlugin runs Fairwinds' pluto to detect deprecated Kubernetes API
// versions across the repo's manifests. Pluto's JSON output flags each
// occurrence with the current API version, the version it was deprecated
// in, and the version it will be removed in.
//
// Critical for cluster-upgrade planning: a Deployment using
// apps/v1beta1 stops working when the cluster reaches 1.16+; pluto
// catches this before you ship.
type PlutoPlugin struct{}

func init() { plugin.Register(&PlutoPlugin{}) }

func (p *PlutoPlugin) Name() string                 { return "pluto" }
func (p *PlutoPlugin) Category() models.Category    { return models.CategoryInfra }
func (p *PlutoPlugin) Languages() []models.Language { return nil }

func (p *PlutoPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *PlutoPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath},
		"pluto", "detect-files", "-d", "/scan", "-o", "json", "--ignore-deprecations=false")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("pluto", err)
	}

	findings, perr := parsePlutoOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type plutoOutput struct {
	Items []plutoItem `json:"items"`
}

type plutoItem struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace"`
	Kind             string `json:"kind"`
	APIVersion       string `json:"api"`
	ReplacementAPI   string `json:"replacement"`
	Deprecated       bool   `json:"deprecated"`
	DeprecatedIn     string `json:"deprecated-in"`
	Removed          bool   `json:"removed"`
	RemovedIn        string `json:"removed-in"`
	Filepath         string `json:"filePath"`
	K8sCurrentVer    string `json:"k8sVersion"`
}

func parsePlutoOutput(data []byte) ([]models.Finding, error) {
	var out plutoOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &out); err != nil {
		return nil, fmt.Errorf("pluto: parse: %w", err)
	}
	var findings []models.Finding
	for _, item := range out.Items {
		if !item.Deprecated && !item.Removed {
			continue
		}
		sev := models.SeverityMedium
		if item.Removed {
			sev = models.SeverityHigh
		}
		title := fmt.Sprintf("%s/%s uses deprecated API %s", item.Kind, item.Name, item.APIVersion)
		if item.Removed {
			title = fmt.Sprintf("%s/%s uses REMOVED API %s (since %s)",
				item.Kind, item.Name, item.APIVersion, item.RemovedIn)
		}
		desc := fmt.Sprintf("API %q was deprecated in Kubernetes %s",
			item.APIVersion, item.DeprecatedIn)
		if item.RemovedIn != "" {
			desc += fmt.Sprintf(" and removed in %s", item.RemovedIn)
		}
		if item.ReplacementAPI != "" {
			desc += fmt.Sprintf("; replace with %q", item.ReplacementAPI)
		}
		findings = append(findings, models.Finding{
			ToolName:    "pluto",
			Category:    models.CategoryInfra,
			Severity:    sev,
			Title:       title,
			Description: desc,
			FilePath:    item.Filepath,
			RuleID:      "pluto-" + item.APIVersion,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
