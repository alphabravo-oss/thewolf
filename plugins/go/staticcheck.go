package goplug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// StaticcheckPlugin runs staticcheck for Go code quality analysis.
type StaticcheckPlugin struct{}

func init() {
	plugin.Register(&StaticcheckPlugin{})
}

func (p *StaticcheckPlugin) Name() string             { return "staticcheck" }
func (p *StaticcheckPlugin) Category() models.Category { return models.CategoryQuality }
func (p *StaticcheckPlugin) Languages() []models.Language {
	return []models.Language{models.LangGo}
}

func (p *StaticcheckPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("staticcheck")
	return err == nil
}

func (p *StaticcheckPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "staticcheck", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "staticcheck", "-f", "json", "./...")
	cmd.Dir = goDir
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("staticcheck", err)
		}
	}

	return parseStaticcheckOutput(out)
}

type staticcheckDiag struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Location struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location"`
	End struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"end"`
	Message string `json:"message"`
}

func parseStaticcheckOutput(data []byte) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var diag staticcheckDiag
		if err := json.Unmarshal(line, &diag); err != nil {
			continue
		}

		findings = append(findings, models.Finding{
			ToolName:    "staticcheck",
			Category:    models.CategoryQuality,
			Severity:    mapStaticcheckSeverity(diag.Severity),
			Title:       diag.Code,
			Description: diag.Message,
			FilePath:    diag.Location.File,
			LineStart:   diag.Location.Line,
			LineEnd:     diag.End.Line,
			RuleID:      diag.Code,
			Status:      models.StatusOpen,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan staticcheck output: %w", err)
	}
	return findings, nil
}

func mapStaticcheckSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}
