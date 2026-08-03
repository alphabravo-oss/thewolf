package container

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// HadolintPlugin runs Hadolint Dockerfile linting.
type HadolintPlugin struct{}

func init() {
	plugin.Register(&HadolintPlugin{})
}

func (p *HadolintPlugin) Name() string                 { return "hadolint" }
func (p *HadolintPlugin) Category() models.Category    { return models.CategoryContainer }
func (p *HadolintPlugin) Languages() []models.Language { return nil }

func (p *HadolintPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *HadolintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Find Dockerfiles in the repo.
	dockerfiles, _ := filepath.Glob(filepath.Join(opts.RepoPath, "**/Dockerfile*"))
	// Also check the root directly since ** doesn't match zero directories in Go's Glob.
	rootDockerfiles, _ := filepath.Glob(filepath.Join(opts.RepoPath, "Dockerfile*"))
	dockerfiles = append(dockerfiles, rootDockerfiles...)
	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, f := range dockerfiles {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
		}
	}
	if len(unique) == 0 {
		plugin.Skipf(opts.OnOutput, "hadolint", "no Dockerfiles found (searched for Dockerfile* in all directories). Add a Dockerfile to enable linting.")
		return nil, nil
	}

	var allFindings []models.Finding
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	for _, df := range unique {
		// Translate host path to /scan-relative container path.
		rel := strings.TrimPrefix(df, opts.RepoPath)
		rel = strings.TrimPrefix(rel, "/")
		containerDF := "/scan/" + rel
		cmd := container.CommandContext(ctx, cfg,
			container.Options{RepoDir: opts.RepoPath},
			"hadolint", "--format", "json", containerDF)
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			continue
		}
		findings, err := parseHadolintOutput(out)
		if err != nil {
			continue
		}
		for i := range findings {
			findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
		}
		allFindings = append(allFindings, findings...)
	}
	return allFindings, nil
}

type hadolintResult struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Column  int    `json:"column"`
	File    string `json:"file"`
	Level   string `json:"level"`
}

func parseHadolintOutput(data []byte) ([]models.Finding, error) {
	var results []hadolintResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse hadolint output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, models.Finding{
			ToolName:    "hadolint",
			Category:    models.CategoryContainer,
			Severity:    mapHadolintSeverity(r.Level),
			Title:       fmt.Sprintf("[%s] %s", r.Code, r.Message),
			Description: r.Message,
			FilePath:    r.File,
			LineStart:   r.Line,
			RuleID:      r.Code,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapHadolintSeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "info":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
