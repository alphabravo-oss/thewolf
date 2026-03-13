package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// KubescapePlugin runs Kubescape Kubernetes security scanning.
type KubescapePlugin struct{}

func init() {
	plugin.Register(&KubescapePlugin{})
}

func (p *KubescapePlugin) Name() string             { return "kubescape" }
func (p *KubescapePlugin) Category() models.Category { return models.CategoryInfra }
func (p *KubescapePlugin) Languages() []models.Language { return nil }

func (p *KubescapePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("kubescape")
	return err == nil
}

func (p *KubescapePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "kubescape", "scan", "--format", "json", "--output", "-", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("kubescape execution failed: %w", err)
		}
	}

	return parseKubescapeOutput(out)
}

type kubescapeOutput struct {
	Results []kubescapeResult `json:"results"`
}

type kubescapeResult struct {
	ResourceID string                  `json:"resourceID"`
	Controls   []kubescapeControlResult `json:"controls"`
}

type kubescapeControlResult struct {
	ControlID string `json:"controlID"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Severity  struct {
		ScoreFactor float64 `json:"scoreFactor"`
	} `json:"severity"`
}

func parseKubescapeOutput(data []byte) ([]models.Finding, error) {
	var output kubescapeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse kubescape output: %w", err)
	}

	var findings []models.Finding
	for _, r := range output.Results {
		for _, c := range r.Controls {
			if c.Status == "passed" {
				continue
			}
			findings = append(findings, models.Finding{
				ToolName:    "kubescape",
				Category:    models.CategoryInfra,
				Severity:    mapKubescapeScore(c.Severity.ScoreFactor),
				Title:       fmt.Sprintf("[%s] %s", c.ControlID, c.Name),
				Description: c.Name,
				FilePath:    r.ResourceID,
				RuleID:      c.ControlID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapKubescapeScore(score float64) models.Severity {
	switch {
	case score >= 8:
		return models.SeverityCritical
	case score >= 6:
		return models.SeverityHigh
	case score >= 4:
		return models.SeverityMedium
	case score >= 2:
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
