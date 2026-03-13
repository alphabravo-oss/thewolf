package cpp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// InferPlugin runs Facebook/Meta Infer for C/C++/Java/ObjC analysis.
type InferPlugin struct{}

func init() {
	plugin.Register(&InferPlugin{})
}

func (p *InferPlugin) Name() string             { return "infer" }
func (p *InferPlugin) Category() models.Category { return models.CategorySAST }
func (p *InferPlugin) Languages() []models.Language {
	return []models.Language{models.LangC, models.LangCPP, models.LangJava, models.LangObjC}
}

func (p *InferPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("infer")
	return err == nil
}

func (p *InferPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFile(opts.RepoPath, "Makefile") {
		plugin.Skipf(opts.OnOutput, "infer", "no Makefile found — Infer requires a build system. Add a Makefile to enable analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "infer", "run", "--", "make")
	cmd.Dir = opts.RepoPath
	if err := cmd.Run(); err != nil {
		return nil, plugin.WrapExecError("infer", err)
	}

	// Read the report
	reportCmd := plugin.CommandContext(ctx, "infer", "report", "--issues-json", "-")
	reportCmd.Dir = opts.RepoPath
	out, err := reportCmd.Output()
	if err != nil {
		return nil, plugin.WrapExecError("infer", err)
	}

	return parseInferOutput(out)
}

type inferIssue struct {
	BugType     string `json:"bug_type"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Procedure   string `json:"procedure"`
	Qualifier   string `json:"qualifier"`
	BugTypeHum  string `json:"bug_type_hum"`
}

func parseInferOutput(data []byte) ([]models.Finding, error) {
	var issues []inferIssue
	if err := json.Unmarshal(plugin.ExtractJSON(data), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse infer output: %w", err)
	}

	findings := make([]models.Finding, 0, len(issues))
	for _, issue := range issues {
		findings = append(findings, models.Finding{
			ToolName:    "infer",
			Category:    models.CategorySAST,
			Severity:    mapInferSeverity(issue.Severity),
			Title:       issue.BugTypeHum,
			Description: issue.Qualifier,
			FilePath:    issue.File,
			LineStart:   issue.Line,
			RuleID:      issue.BugType,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapInferSeverity(s string) models.Severity {
	switch s {
	case "ERROR":
		return models.SeverityHigh
	case "WARNING":
		return models.SeverityMedium
	case "INFO", "ADVICE":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
