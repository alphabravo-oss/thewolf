package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// HadolintPlugin runs Hadolint Dockerfile linting.
type HadolintPlugin struct{}

func init() {
	plugin.Register(&HadolintPlugin{})
}

func (p *HadolintPlugin) Name() string               { return "hadolint" }
func (p *HadolintPlugin) Category() models.Category   { return models.CategoryContainer }
func (p *HadolintPlugin) Languages() []models.Language { return nil }

func (p *HadolintPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("hadolint")
	return err == nil
}

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
		return nil, nil // No Dockerfiles to lint.
	}

	var allFindings []models.Finding
	for _, df := range unique {
		args := []string{"--format", "json", df}
		cmd := exec.CommandContext(ctx, "hadolint", args...)
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			continue // Skip files that hadolint can't process.
		}
		findings, err := parseHadolintOutput(out)
		if err != nil {
			continue
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
	if err := json.Unmarshal(data, &results); err != nil {
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
