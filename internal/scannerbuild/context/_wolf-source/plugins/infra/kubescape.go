package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (p *KubescapePlugin) Applicability(repoPath, _ string) error {
	return plugin.KubernetesApplicability(repoPath)
}

func (p *KubescapePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if err := p.Applicability(opts.RepoPath, opts.Target); err != nil {
		return nil, err
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cache := "/tmp/.kubescape"
	if cfg != nil && cfg.DBVolume != "" {
		cache = "/var/lib/wolf-db/kubescape"
	}
	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath, ExtraEnv: map[string]string{"HOME": "/tmp", "KS_CACHE_DIR": cache}},
		// Omitting --output writes to stdout; "-" is a literal filename.
		"kubescape", "scan", "--format", "json", "/scan")
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("kubescape", err)
		}
	}
	plugin.SaveRaw(opts, out, "json")

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
	Severity  json.RawMessage `json:"severity"`
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
				Severity:    kubescapeSeverity(c.Severity),
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

func kubescapeSeverity(raw json.RawMessage) models.Severity {
	var label string
	if json.Unmarshal(raw, &label) == nil {
		switch models.Severity(strings.ToLower(label)) {
		case models.SeverityCritical, models.SeverityHigh, models.SeverityMedium, models.SeverityLow, models.SeverityInfo:
			return models.Severity(strings.ToLower(label))
		}
	}
	var legacy struct {
		ScoreFactor float64 `json:"scoreFactor"`
	}
	_ = json.Unmarshal(raw, &legacy)
	return mapKubescapeScore(legacy.ScoreFactor)
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
