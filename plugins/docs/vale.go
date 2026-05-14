package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// valeConfigPresent reports whether the repo (or its parent dir, mirroring
// vale's own lookup) has a Vale config file. Vale recognizes .vale.ini and
// _vale.ini; we check both.
func valeConfigPresent(repoPath string) bool {
	for _, name := range []string{".vale.ini", "_vale.ini"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true
		}
	}
	return false
}

// ValePlugin runs Vale documentation style checking.
type ValePlugin struct{}

func init() {
	plugin.Register(&ValePlugin{})
}

func (p *ValePlugin) Name() string               { return "vale" }
func (p *ValePlugin) Category() models.Category   { return models.CategoryDocs }
func (p *ValePlugin) Languages() []models.Language { return nil }

func (p *ValePlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ValePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "md", "rst", "txt") {
		plugin.Skipf(opts.OnOutput, "vale", "no documentation files (*.md, *.rst) found. Add Markdown or reStructuredText files to enable prose linting.")
		return nil, nil
	}
	// vale exits with E100 "no config file found" and a non-zero status
	// when neither a project-level .vale.ini nor a workspace ancestor
	// provides one. That's a *user* configuration choice, not a wolf
	// failure — skip cleanly so the scan doesn't appear broken.
	if !valeConfigPresent(opts.RepoPath) {
		plugin.Skipf(opts.OnOutput, "vale", "no .vale.ini found in repo root. Create one to enable prose linting (https://vale.sh/docs/topics/config/).")
		return nil, nil
	}

	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"vale", "--output", "JSON", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("vale", err)
	}

	findings, perr := parseValeOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// Vale outputs: { "file.md": [ { ... }, ... ] }
type valeAlert struct {
	Action struct {
		Name   string `json:"name"`
		Params []string `json:"params"`
	} `json:"Action"`
	Check    string `json:"Check"`
	Line     int    `json:"Line"`
	Link     string `json:"Link"`
	Message  string `json:"Message"`
	Severity string `json:"Severity"`
	Span     []int  `json:"Span"`
	Match    string `json:"Match"`
}

func parseValeOutput(data []byte) ([]models.Finding, error) {
	var output map[string][]valeAlert
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse vale output: %w", err)
	}

	var findings []models.Finding
	for filePath, alerts := range output {
		for _, a := range alerts {
			findings = append(findings, models.Finding{
				ToolName:    "vale",
				Category:    models.CategoryDocs,
				Severity:    mapValeSeverity(a.Severity),
				Title:       fmt.Sprintf("[%s] %s", a.Check, a.Message),
				Description: a.Message,
				FilePath:    filePath,
				LineStart:   a.Line,
				CodeSnippet: a.Match,
				RuleID:      a.Check,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapValeSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "suggestion":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
