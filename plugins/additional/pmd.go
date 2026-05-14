package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// PMDPlugin runs PMD code quality analysis.
type PMDPlugin struct{}

func init() {
	plugin.Register(&PMDPlugin{})
}

func (p *PMDPlugin) Name() string               { return "pmd" }
func (p *PMDPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *PMDPlugin) Languages() []models.Language { return nil }

func (p *PMDPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *PMDPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// PMD ships in the JVM bucket image (with infer). It's not in the
	// default wolf-scanners image. If the operator hasn't built the JVM
	// bucket and pointed WOLF_SCANNERS_IMAGE_JVM at it, skip cleanly
	// instead of failing with "tool not present in image".
	if !cfg.HasDedicatedImage("pmd") {
		plugin.Skipf(opts.OnOutput, "pmd",
			"not configured. PMD lives in the JVM bucket image; run `make scanners-build-jvm` then set WOLF_SCANNERS_IMAGE_JVM=wolf-scanners-jvm:dev. Skipping.")
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
		"pmd", "check",
		"--format", "json",
		"-d", "/scan",
		"--rulesets", "rulesets/java/quickstart.xml,rulesets/ecmascript/quickstart.xml",
		"--no-progress")
	out, err := cmd.Output()
	if err != nil {
		// PMD exits with code 4 when violations are found — that's normal.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 4 && len(out) > 0 {
			// fall through to parse
		} else if len(out) == 0 {
			return nil, plugin.WrapExecError("pmd", err)
		}
	}

	findings, perr := parsePMDOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
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
