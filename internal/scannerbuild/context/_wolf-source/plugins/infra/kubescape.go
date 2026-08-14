package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// KubescapePlugin runs Kubescape Kubernetes security scanning.
type KubescapePlugin struct{}

func init() {
	plugin.Register(&KubescapePlugin{})
}

func (p *KubescapePlugin) Name() string                 { return "kubescape" }
func (p *KubescapePlugin) Category() models.Category    { return models.CategoryInfra }
func (p *KubescapePlugin) Languages() []models.Language { return nil }

func (p *KubescapePlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *KubescapePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"kubescape", "scan", "--format", "json", "--output", "-", "/scan")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no resources found") {
				plugin.Skipf(opts.OnOutput, "kubescape", "no Kubernetes manifests found in this repository. Add YAML files with Kubernetes resources (Deployments, Services, etc.) to enable scanning.")
				return nil, nil
			}
		}
		if len(out) == 0 {
			return nil, plugin.WrapExecError("kubescape", err)
		}
	}

	findings, perr := parseKubescapeOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type kubescapeOutput struct {
	Results []kubescapeResult `json:"results"`
}

type kubescapeResult struct {
	ResourceID string                   `json:"resourceID"`
	Controls   []kubescapeControlResult `json:"controls"`
}

type kubescapeControlResult struct {
	ControlID string          `json:"controlID"`
	Name      string          `json:"name"`
	Status    kubescapeStatus `json:"status"`
	Severity  struct {
		ScoreFactor float64 `json:"scoreFactor"`
	} `json:"severity"`
}

// kubescapeStatus accepts both the legacy string ("failed") and current
// object form ({"status":"failed","subStatus":"...","info":"..."}).
type kubescapeStatus string

func (s *kubescapeStatus) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*s = ""
		return nil
	}
	if data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*s = kubescapeStatus(str)
		return nil
	}
	var obj struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	val := obj.Status
	if val == "" {
		val = obj.Code
	}
	*s = kubescapeStatus(val)
	return nil
}

func (s kubescapeStatus) String() string { return string(s) }

func (s kubescapeStatus) passed() bool {
	switch strings.ToLower(strings.TrimSpace(string(s))) {
	case "passed", "pass", "success", "ok", "skipped", "skip", "irrelevant":
		return true
	default:
		return false
	}
}

func parseKubescapeOutput(data []byte) ([]models.Finding, error) {
	var output kubescapeOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse kubescape output: %w", err)
	}

	var findings []models.Finding
	for _, r := range output.Results {
		for _, c := range r.Controls {
			if c.Status.passed() {
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
