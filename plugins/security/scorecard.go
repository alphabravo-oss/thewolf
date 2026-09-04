package security

import (
	"context"
	"encoding/json"
	"fmt"
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
			Category:    models.CategorySAST,
			Severity:    mapScorecardScore(c.Score),
			Title:       fmt.Sprintf("%s (score: %d/10)", c.Name, c.Score),
			Description: c.Reason + "\n\n" + strings.Join(c.Details, "\n") + "\nDocs: " + c.Documentation.URL,
			FilePath:    "", // repo-level
			RuleID:      c.Name,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapScorecardScore(score int) models.Severity {
	switch {
	case score < 0:
		return models.SeverityInfo // -1 means "could not be measured"
	case score <= 2:
		return models.SeverityCritical
	case score <= 4:
		return models.SeverityHigh
	case score <= 6:
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}
