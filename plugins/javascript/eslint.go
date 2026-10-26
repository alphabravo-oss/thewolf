package javascript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// ESLintPlugin runs ESLint for JavaScript and TypeScript quality checks.
type ESLintPlugin struct{}

func init() {
	plugin.Register(&ESLintPlugin{})
}

func (p *ESLintPlugin) Name() string              { return "eslint" }
func (p *ESLintPlugin) Category() models.Category { return models.CategoryQuality }
func (p *ESLintPlugin) Languages() []models.Language {
	return []models.Language{models.LangJavaScript, models.LangTypeScript}
}

func (p *ESLintPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ESLintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if err := p.Applicability(opts.RepoPath, ""); err != nil {
		return nil, err
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"eslint", "/scan", "-f", "json")
	out, err := cmd.Output()
	if err != nil {
		// ESLint v9+ exits with code 2 when no config file is found.
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "eslint.config") || strings.Contains(stderr, "eslintrc") {
				return nil, &plugin.NotApplicableError{Reason: eslintNoConfig}
			}
		}
		if len(out) == 0 {
			return nil, plugin.WrapExecError("eslint", err)
		}
	}

	findings, perr := parseESLintOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type eslintFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

type eslintMessage struct {
	RuleID   string `json:"ruleId"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Line     int    `json:"line"`
	EndLine  int    `json:"endLine"`
}

func parseESLintOutput(data []byte) ([]models.Finding, error) {
	var files []eslintFile
	if err := json.Unmarshal(plugin.ExtractJSON(data), &files); err != nil {
		return nil, fmt.Errorf("failed to parse eslint output: %w", err)
	}

	var findings []models.Finding
	for _, f := range files {
		for _, m := range f.Messages {
			findings = append(findings, models.Finding{
				ToolName:    "eslint",
				Category:    models.CategoryQuality,
				Severity:    mapESLintSeverity(m.Severity),
				Title:       m.RuleID,
				Description: m.Message,
				FilePath:    f.FilePath,
				LineStart:   m.Line,
				LineEnd:     m.EndLine,
				RuleID:      m.RuleID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapESLintSeverity(s int) models.Severity {
	switch s {
	case 2:
		return models.SeverityHigh
	case 1:
		return models.SeverityMedium
	default:
		return models.SeverityInfo
	}
}

const eslintNoConfig = "no ESLint configuration found. Create an eslint.config.js (ESLint v9+) or .eslintrc.* file in the repository root to enable linting."

// Applicability keeps absent inputs out of selection and coverage. ESLint
// resolves configuration from the repository root.
func (p *ESLintPlugin) Applicability(repoPath, _ string) error {
	return plugin.Absent(!plugin.HasGlob(repoPath, "eslint.config.*", ".eslintrc", ".eslintrc.*") && !packageJSONHasESLintConfig(repoPath), eslintNoConfig)
}

func packageJSONHasESLintConfig(repoPath string) bool {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		ESLintConfig json.RawMessage `json:"eslintConfig"`
	}
	return json.Unmarshal(data, &manifest) == nil && len(manifest.ESLintConfig) > 0
}
