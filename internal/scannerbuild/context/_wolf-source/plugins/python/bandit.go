package python

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// BanditPlugin runs Bandit security linter for Python.
//
// Post-containerization:
//   - CheckAvailable asks the container backend, not the host PATH.
//   - Execute runs bandit inside wolf-scanners with the repo bind-mounted
//     read-only at /scan.
//   - Findings' file paths are normalized from /scan/<rel> to <rel>.
type BanditPlugin struct{}

func init() { plugin.Register(&BanditPlugin{}) }

func (p *BanditPlugin) Name() string                 { return "bandit" }
func (p *BanditPlugin) Category() models.Category    { return models.CategorySAST }
func (p *BanditPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *BanditPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *BanditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "py") {
		plugin.Skipf(opts.OnOutput, "bandit", "no Python files (*.py) found. Add Python source files to enable security analysis.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"bandit", "-r", "/scan", "-f", "json", "--exit-zero")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("bandit", err)
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseBanditOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type banditOutput struct {
	Results []banditResult `json:"results"`
}

type banditResult struct {
	TestID          string `json:"test_id"`
	TestName        string `json:"test_name"`
	IssueSeverity   string `json:"issue_severity"`
	IssueConfidence string `json:"issue_confidence"`
	IssueText       string `json:"issue_text"`
	Filename        string `json:"filename"`
	LineNumber      int    `json:"line_number"`
	EndColOffset    int    `json:"end_col_offset"`
	IssueCWE        struct {
		ID int `json:"id"`
	} `json:"issue_cwe"`
	MoreInfo string `json:"more_info"`
}

func parseBanditOutput(data []byte) ([]models.Finding, error) {
	var output banditOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse bandit output: %w", err)
	}
	findings := make([]models.Finding, 0, len(output.Results))
	for _, r := range output.Results {
		cweID := ""
		if r.IssueCWE.ID > 0 {
			cweID = fmt.Sprintf("CWE-%d", r.IssueCWE.ID)
		}
		findings = append(findings, models.Finding{
			ToolName:    "bandit",
			Category:    models.CategorySAST,
			Severity:    mapBanditSeverity(r.IssueSeverity, r.IssueConfidence),
			Title:       r.TestName,
			Description: r.IssueText,
			FilePath:    r.Filename,
			LineStart:   r.LineNumber,
			LineEnd:     r.LineNumber,
			RuleID:      r.TestID,
			CWEID:       cweID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapBanditSeverity(severity, confidence string) models.Severity {
	s := strings.ToUpper(severity)
	c := strings.ToUpper(confidence)
	if s == "HIGH" && c == "HIGH" {
		return models.SeverityCritical
	}
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
