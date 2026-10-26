package security

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// ScorecardPlugin runs OpenSSF Scorecard against a repository to score the
// repo's overall security hygiene (branch protection, signed releases,
// pinned deps, CI configuration, etc.).
//
// Unlike per-file SAST tools, scorecard emits *repo-level* findings keyed by
// the check name (e.g. "Branch-Protection", "Signed-Releases"). FilePath is
// left empty; the finding applies to the whole repo.
//
// scorecard requires either a public GitHub URL OR a local repo path that
// contains a .git directory. Wolf's repo is bind-mounted at /scan as-is;
// scorecard runs in --local mode against /scan.
type ScorecardPlugin struct{}

func init() { plugin.Register(&ScorecardPlugin{}) }

func (p *ScorecardPlugin) Name() string                 { return "scorecard" }
func (p *ScorecardPlugin) Category() models.Category    { return models.CategorySAST }
func (p *ScorecardPlugin) Languages() []models.Language { return nil }

func (p *ScorecardPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ScorecardPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFile(opts.RepoPath, ".git") {
		plugin.Skipf(opts.OnOutput, "scorecard", "no .git directory — scorecard needs a git repository to inspect (branch protection, signed releases, etc).")
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
		"scorecard", "--local", "/scan", "--format", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("scorecard", err)
	}

	return parseScorecardOutput(out)
}

type scorecardOutput struct {
	Date      string                   `json:"date"`
	Repo      struct{ Name string }    `json:"repo"`
	Scorecard struct{ Version string } `json:"scorecard"`
	Score     float64                  `json:"score"`
	Checks    []scorecardCheck         `json:"checks"`
}

type scorecardCheck struct {
	Name          string `json:"name"`
	Score         int    `json:"score"` // -1 to 10
	Reason        string `json:"reason"`
	Documentation struct {
		URL string `json:"url"`
	} `json:"documentation"`
	Details []string `json:"details"`
}

func parseScorecardOutput(data []byte) ([]models.Finding, error) {
	var output scorecardOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse scorecard output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Checks))
	for _, c := range output.Checks {
		// Only emit findings for checks that scored < 7 (the OpenSSF
		// threshold for "needs attention"). Skip checks that passed.
		if c.Score >= 7 {
			continue
		}
		findings = append(findings, models.Finding{
			ToolName:    "scorecard",
			Category:    scorecardCategory(c.Name),
			Severity:    mapScorecardScore(c.Name, c.Score),
			Title:       fmt.Sprintf("%s (score: %d/10)", c.Name, c.Score),
			Description: c.Reason + "\n\n" + strings.Join(c.Details, "\n") + "\nDocs: " + c.Documentation.URL,
			FilePath:    "", // repo-level
			RuleID:      c.Name,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

// scorecardRisk is OpenSSF Scorecard's documented risk level per check
// (https://github.com/ossf/scorecard/blob/main/docs/checks.md). Unknown checks are medium.
var scorecardRisk = map[string]models.Severity{
	"Dangerous-Workflow": models.SeverityCritical, "Webhooks": models.SeverityCritical,
	"Binary-Artifacts": models.SeverityHigh, "Branch-Protection": models.SeverityHigh, "Code-Review": models.SeverityHigh,
	"Dependency-Update-Tool": models.SeverityHigh, "Maintained": models.SeverityHigh, "Signed-Releases": models.SeverityHigh,
	"Token-Permissions": models.SeverityHigh, "Vulnerabilities": models.SeverityHigh,
	"Fuzzing": models.SeverityMedium, "Packaging": models.SeverityMedium, "Pinned-Dependencies": models.SeverityMedium,
	"SAST": models.SeverityMedium, "Security-Policy": models.SeverityMedium,
	"CI-Tests": models.SeverityLow, "CII-Best-Practices": models.SeverityLow, "Contributors": models.SeverityLow, "License": models.SeverityLow,
}

// Scorecard grades practice adoption, not exploitable code, so checks are
// posture (quality) except Dangerous-Workflow, an injectable CI workflow.
func scorecardCategory(check string) models.Category {
	if check == "Dangerous-Workflow" {
		return models.CategorySAST
	}
	return models.CategoryQuality
}

// Severity is the check's own risk level, lowered one step per partial
// adoption band (0-2 full risk, 3-5 one lower, 6 two lower).
func mapScorecardScore(check string, score int) models.Severity {
	if score < 0 {
		return models.SeverityInfo // -1 means "could not be measured"
	}
	order := []models.Severity{models.SeverityCritical, models.SeverityHigh, models.SeverityMedium, models.SeverityLow, models.SeverityInfo}
	risk, ok := scorecardRisk[check]
	if !ok {
		risk = models.SeverityMedium
	}
	i := slices.Index(order, risk) + score/3
	return order[min(i, len(order)-1)]
}
