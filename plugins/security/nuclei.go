package security

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// NucleiPlugin runs Nuclei template-based vulnerability scanning.
type NucleiPlugin struct{}

func init() {
	plugin.Register(&NucleiPlugin{})
}

func (p *NucleiPlugin) Name() string             { return "nuclei" }
func (p *NucleiPlugin) Category() models.Category { return models.CategoryDAST }
func (p *NucleiPlugin) Languages() []models.Language { return nil }

func (p *NucleiPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *NucleiPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Nuclei is a DAST scanner — it scans HTTP targets, not local repos.
	// When a target URL is provided, no repo mount is needed.
	// When only a target list file in the repo is provided, mount it.
	args := []string{"-jsonl", "-silent"}
	opt := container.Options{NoRepoMount: opts.Target != ""}
	if opts.Target != "" {
		args = append(args, "-u", opts.Target)
	} else {
		opt.RepoDir = opts.RepoPath
		args = append(args, "-l", "/scan")
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		opt,
		"nuclei", args...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("nuclei", err)
	}

	findings, perr := parseNucleiOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type nucleiResult struct {
	TemplateID string `json:"template-id"`
	Info       struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Severity    string   `json:"severity"`
		Tags        []string `json:"tags"`
		Reference   []string `json:"reference"`
		CWE         []string `json:"cwe"`
	} `json:"info"`
	MatcherName string `json:"matcher-name"`
	Host        string `json:"host"`
	MatchedAt   string `json:"matched-at"`
}

func parseNucleiOutput(data []byte) ([]models.Finding, error) {
	var findings []models.Finding

	// NDJSON — one result per line
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var r nucleiResult
		if err := dec.Decode(&r); err != nil {
			continue
		}
		cwe := ""
		if len(r.Info.CWE) > 0 {
			cwe = r.Info.CWE[0]
		}

		findings = append(findings, models.Finding{
			ToolName:    "nuclei",
			Category:    models.CategoryDAST,
			Severity:    mapNucleiSeverity(r.Info.Severity),
			Title:       r.Info.Name,
			Description: r.Info.Description,
			FilePath:    r.MatchedAt,
			CWEID:       cwe,
			RuleID:      r.TemplateID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapNucleiSeverity(s string) models.Severity {
	switch s {
	case "critical":
		return models.SeverityCritical
	case "high":
		return models.SeverityHigh
	case "medium":
		return models.SeverityMedium
	case "low":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
