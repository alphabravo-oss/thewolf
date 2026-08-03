package sbom

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// SyftPlugin runs Syft SBOM generation.
type SyftPlugin struct{}

func init() {
	plugin.Register(&SyftPlugin{})
}

func (p *SyftPlugin) Name() string                 { return "syft" }
func (p *SyftPlugin) Category() models.Category    { return models.CategorySBOM }
func (p *SyftPlugin) Languages() []models.Language { return nil }

func (p *SyftPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *SyftPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"syft", "/scan", "-o", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("syft", err)
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseSyftOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type syftOutput struct {
	Artifacts []syftArtifact `json:"artifacts"`
}

type syftArtifact struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Licenses []struct {
		Value string `json:"value"`
	} `json:"licenses"`
	Locations []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

// parseSyftOutput validates that the syft JSON is well-formed but does
// NOT emit Findings rows — syft produces an SBOM (inventory), not
// vulnerabilities. Emitting one Finding per package as severity=info
// polluted the findings table with hundreds of non-issues that the UI
// then had to filter. The SBOM itself is preserved via the standard
// scanner-artifact persistence (syft.json under the scan artifact dir),
// which is the correct place to surface inventory data.
func parseSyftOutput(data []byte) ([]models.Finding, error) {
	var output syftOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse syft output: %w", err)
	}
	// Output is intentionally discarded after validation — see comment above.
	_ = output
	return nil, nil
}
