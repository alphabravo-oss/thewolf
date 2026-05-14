package sql

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

// sqlfluffConfigFiles lists the config-file names sqlfluff recognizes.
// If any of them is present in the repo root, sqlfluff reads the dialect
// from there and we DON'T pass --dialect on the CLI.
var sqlfluffConfigFiles = []string{".sqlfluff", "pyproject.toml", "tox.ini", "setup.cfg"}

// SQLFluffPlugin runs SQLFluff linting on SQL files.
type SQLFluffPlugin struct{}

func init() {
	plugin.Register(&SQLFluffPlugin{})
}

func (p *SQLFluffPlugin) Name() string             { return "sqlfluff" }
func (p *SQLFluffPlugin) Category() models.Category { return models.CategoryQuality }
func (p *SQLFluffPlugin) Languages() []models.Language {
	return []models.Language{models.LangSQL}
}

func (p *SQLFluffPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *SQLFluffPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "sql") {
		plugin.Skipf(opts.OnOutput, "sqlfluff", "no SQL files (*.sql) found. Add SQL files to enable linting.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// sqlfluff refuses to run without a dialect. If the repo carries a
	// config file (.sqlfluff, pyproject.toml, etc.) sqlfluff reads the
	// dialect from there. Otherwise we default to "ansi" — the most
	// permissive dialect — so the scanner produces *some* output instead
	// of an "exit 2: User Error: No dialect was specified" failure.
	args := []string{"lint", "--format", "json", "/scan"}
	if !sqlfluffHasConfig(opts.RepoPath) {
		args = append([]string{"lint", "--dialect", "ansi", "--format", "json", "/scan"}, nil...)
	}
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"sqlfluff", args...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("sqlfluff", err)
	}

	findings, perr := parseSQLFluffOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type sqlfluffResult struct {
	Filepath   string             `json:"filepath"`
	Violations []sqlfluffViolation `json:"violations"`
}

type sqlfluffViolation struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	LineNo      int    `json:"start_line_no"`
	LinePos     int    `json:"start_line_pos"`
}

// sqlfluffHasConfig returns true when a sqlfluff-recognised config file
// exists at the repo root — sqlfluff will read the dialect from there.
func sqlfluffHasConfig(repoPath string) bool {
	for _, name := range sqlfluffConfigFiles {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			// pyproject.toml only counts if it has a [tool.sqlfluff] section;
			// best-effort detection without parsing TOML.
			if name == "pyproject.toml" {
				// #nosec G304 -- checks for a sqlfluff config file at the scan root
				data, _ := os.ReadFile(filepath.Join(repoPath, name))
				if string(data) == "" {
					return false
				}
				if !containsAny(data, "[tool.sqlfluff", "sqlfluff") {
					continue
				}
			}
			return true
		}
	}
	return false
}

func containsAny(haystack []byte, needles ...string) bool {
	s := string(haystack)
	for _, n := range needles {
		if len(s) > 0 && len(n) > 0 && indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func parseSQLFluffOutput(data []byte) ([]models.Finding, error) {
	var results []sqlfluffResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse sqlfluff output: %w", err)
	}

	var findings []models.Finding
	for _, r := range results {
		for _, v := range r.Violations {
			findings = append(findings, models.Finding{
				ToolName:    "sqlfluff",
				Category:    models.CategoryQuality,
				Severity:    models.SeverityLow,
				Title:       fmt.Sprintf("[%s] %s", v.Code, v.Description),
				Description: v.Description,
				FilePath:    r.Filepath,
				LineStart:   v.LineNo,
				RuleID:      v.Code,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
