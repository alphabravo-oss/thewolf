package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// PMDPlugin runs PMD code quality analysis.
type PMDPlugin struct{}

func init() {
	plugin.Register(&PMDPlugin{})
}

func (p *PMDPlugin) Name() string               { return "pmd" }
func (p *PMDPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *PMDPlugin) Languages() []models.Language { return nil }

func (p *PMDPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("pmd")
	return err == nil
}

func (p *PMDPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{
		"check",
		"--format", "json",
		"-d", opts.RepoPath,
		"--rulesets", "rulesets/java/quickstart.xml,rulesets/ecmascript/quickstart.xml",
		"--no-progress",
	}
	cmd := plugin.CommandContext(ctx, "pmd", args...)
	out, err := cmd.Output()
	if err != nil {
		// PMD exits with code 4 when violations are found — that's normal.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 4 {
			if len(out) > 0 {
				return parsePMDOutput(out)
			}
		}
		if len(out) == 0 {
			return nil, plugin.WrapExecError("pmd", err)
		}
	}

	return parsePMDOutput(out)
}

type pmdOutput struct {
	Files []pmdFile `json:"files"`
}

type pmdFile struct {
	Filename   string         `json:"filename"`
	Violations []pmdViolation `json:"violations"`
}

type pmdViolation struct {
	BeginLine   int    `json:"beginline"`
	EndLine     int    `json:"endline"`
	Description string `json:"description"`
	Rule        string `json:"rule"`
	RuleSet     string `json:"ruleset"`
	Priority    int    `json:"priority"`
	ExternalURL string `json:"externalInfoUrl"`
}

func parsePMDOutput(data []byte) ([]models.Finding, error) {
	var output pmdOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse pmd output: %w", err)
	}

	var findings []models.Finding
	for _, f := range output.Files {
		for _, v := range f.Violations {
			findings = append(findings, models.Finding{
				ToolName:    "pmd",
				Category:    models.CategoryQuality,
				Severity:    mapPMDPriority(v.Priority),
				Title:       fmt.Sprintf("[%s] %s", v.Rule, v.Description),
				Description: v.Description,
				FilePath:    f.Filename,
				LineStart:   v.BeginLine,
				LineEnd:     v.EndLine,
				RuleID:      v.Rule,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapPMDPriority(priority int) models.Severity {
	switch priority {
	case 1:
		return models.SeverityCritical
	case 2:
		return models.SeverityHigh
	case 3:
		return models.SeverityMedium
	case 4:
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
