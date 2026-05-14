package goplug

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// GoKartPlugin runs Praetorian's GoKart, a source-to-sink taint-analysis
// SAST for Go. Complements gosec (pattern-based): gosec flags constructs
// that LOOK risky; GoKart traces user input through the program and only
// flags constructs that ACTUALLY receive tainted data.
//
// GoKart writes JSON to stdout via the `-r` / `-o` flag.
type GoKartPlugin struct{}

func init() { plugin.Register(&GoKartPlugin{}) }

func (p *GoKartPlugin) Name() string                 { return "gokart" }
func (p *GoKartPlugin) Category() models.Category    { return models.CategorySAST }
func (p *GoKartPlugin) Languages() []models.Language { return []models.Language{models.LangGo} }

func (p *GoKartPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *GoKartPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "gokart", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, goDir),
		},
		"gokart", "scan", "-o", "json", "./...")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("gokart", err)
	}

	findings, perr := parseGoKartOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// gokartResult is the SARIF-like JSON shape gokart emits.
type gokartResult struct {
	Runs []struct {
		Results []struct {
			RuleID    string `json:"ruleId"`
			Message   struct {
				Text string `json:"text"`
			} `json:"message"`
			Level     string `json:"level"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine int `json:"startLine"`
						EndLine   int `json:"endLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func parseGoKartOutput(data []byte) ([]models.Finding, error) {
	var sarif gokartResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &sarif); err != nil {
		return nil, fmt.Errorf("gokart: parse: %w", err)
	}
	var findings []models.Finding
	for _, run := range sarif.Runs {
		for _, r := range run.Results {
			file := ""
			ls, le := 0, 0
			if len(r.Locations) > 0 {
				loc := r.Locations[0].PhysicalLocation
				file = loc.ArtifactLocation.URI
				ls = loc.Region.StartLine
				le = loc.Region.EndLine
			}
			findings = append(findings, models.Finding{
				ToolName:    "gokart",
				Category:    models.CategorySAST,
				Severity:    mapGoKartSeverity(r.Level),
				Title:       r.RuleID,
				Description: r.Message.Text,
				FilePath:    file,
				LineStart:   ls,
				LineEnd:     le,
				RuleID:      r.RuleID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapGoKartSeverity(level string) models.Severity {
	switch strings.ToLower(level) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityMedium
	}
}
