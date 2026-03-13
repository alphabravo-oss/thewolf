package sbom

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// SyftPlugin runs Syft SBOM generation.
type SyftPlugin struct{}

func init() {
	plugin.Register(&SyftPlugin{})
}

func (p *SyftPlugin) Name() string               { return "syft" }
func (p *SyftPlugin) Category() models.Category   { return models.CategorySBOM }
func (p *SyftPlugin) Languages() []models.Language { return nil }

func (p *SyftPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("syft")
	return err == nil
}

func (p *SyftPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{opts.RepoPath, "-o", "json"}
	cmd := plugin.CommandContext(ctx, "syft", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("syft", err)
		}
	}

	return parseSyftOutput(out)
}

type syftOutput struct {
	Artifacts []syftArtifact `json:"artifacts"`
}

type syftArtifact struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Type      string `json:"type"`
	Language  string `json:"language"`
	Licenses  []struct {
		Value string `json:"value"`
	} `json:"licenses"`
	Locations []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

func parseSyftOutput(data []byte) ([]models.Finding, error) {
	var output syftOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse syft output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Artifacts))
	for _, a := range output.Artifacts {
		filePath := ""
		if len(a.Locations) > 0 {
			filePath = a.Locations[0].Path
		}

		license := ""
		if len(a.Licenses) > 0 {
			license = a.Licenses[0].Value
		}

		findings = append(findings, models.Finding{
			ToolName:    "syft",
			Category:    models.CategorySBOM,
			Severity:    models.SeverityInfo,
			Title:       fmt.Sprintf("Package: %s@%s (%s)", a.Name, a.Version, a.Type),
			Description: fmt.Sprintf("Package %s version %s, type: %s, language: %s, license: %s", a.Name, a.Version, a.Type, a.Language, license),
			FilePath:    filePath,
			RuleID:      fmt.Sprintf("sbom-%s-%s", a.Name, a.Version),
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
