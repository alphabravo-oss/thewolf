package goplug

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// GosecPlugin runs gosec static analysis for Go code.
type GosecPlugin struct{}

func init() {
	plugin.Register(&GosecPlugin{})
}

func (p *GosecPlugin) Name() string             { return "gosec" }
func (p *GosecPlugin) Category() models.Category { return models.CategorySAST }
func (p *GosecPlugin) Languages() []models.Language {
	return []models.Language{models.LangGo}
}

func (p *GosecPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("gosec")
	return err == nil
}

func (p *GosecPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "gosec", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "gosec", "-fmt", "json", "./...")
	cmd.Dir = goDir
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("gosec", err)
		}
	}

	return parseGosecOutput(out)
}

type gosecOutput struct {
	Issues []gosecIssue `json:"Issues"`
}

type gosecIssue struct {
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	CWE        struct {
		ID string `json:"id"`
	} `json:"cwe"`
	RuleID  string `json:"rule_id"`
	Details string `json:"details"`
	File    string `json:"file"`
	Line    string `json:"line"`
	Column  string `json:"column"`
	Code    string `json:"code"`
}

func parseGosecOutput(data []byte) ([]models.Finding, error) {
	var output gosecOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse gosec output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Issues))
	for _, issue := range output.Issues {
		lineStart, _ := strconv.Atoi(issue.Line)

		findings = append(findings, models.Finding{
			ToolName:    "gosec",
			Category:    models.CategorySAST,
			Severity:    mapGosecSeverity(issue.Severity),
			Title:       issue.RuleID,
			Description: issue.Details,
			FilePath:    issue.File,
			LineStart:   lineStart,
			CodeSnippet: issue.Code,
			CWEID:       issue.CWE.ID,
			RuleID:      issue.RuleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapGosecSeverity(s string) models.Severity {
	switch s {
	case "HIGH":
		return models.SeverityHigh
	case "MEDIUM":
		return models.SeverityMedium
	case "LOW":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
