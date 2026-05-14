package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// KICSPlugin runs Checkmarx KICS for multi-format IaC SAST.
//
// KICS covers Terraform, Kubernetes, Dockerfile, CloudFormation, Ansible,
// Helm, ARM, OpenAPI, Pulumi, gRPC, Knative, Crossplane — ~3000 rules.
// Where Trivy and Checkov touch broadly, KICS goes deep on each format.
//
// The kics image's entrypoint is the kics binary. We run:
//
//	kics scan -p /scan -o /tmp/kics --no-progress --report-formats json
//
// then read /tmp/kics/results.json. Because /tmp is the per-container
// tmpfs, we wrap the invocation in `sh -c` and `cat` the result.
type KICSPlugin struct{}

func init() { plugin.Register(&KICSPlugin{}) }

func (p *KICSPlugin) Name() string                 { return "kics" }
func (p *KICSPlugin) Category() models.Category    { return models.CategoryInfra }
func (p *KICSPlugin) Languages() []models.Language { return nil }

func (p *KICSPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *KICSPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// Wrap in sh -c so we can read the results file after kics writes it.
	script := "kics scan -p /scan -o /tmp/kics --no-progress " +
		"--report-formats json --silent >/dev/null 2>&1; " +
		"cat /tmp/kics/results.json"
	// EntrypointOverride="sh" — the runner skips the tool-name dispatcher
	// when the override is set, so the args go straight to /bin/sh.
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir:            opts.RepoPath,
			EntrypointOverride: "sh",
		},
		"kics", "-c", script)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("kics", err)
	}

	findings, perr := parseKICSOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type kicsOutput struct {
	Queries []kicsQuery `json:"queries"`
}

type kicsQuery struct {
	QueryName    string    `json:"query_name"`
	QueryID      string    `json:"query_id"`
	Severity     string    `json:"severity"`
	Category     string    `json:"category"`
	Description  string    `json:"description"`
	Platform     string    `json:"platform"`
	CWE          string    `json:"cwe"`
	Files        []kicsHit `json:"files"`
}

type kicsHit struct {
	FileName     string `json:"file_name"`
	Line         int    `json:"line"`
	IssueType    string `json:"issue_type"`
	SearchKey    string `json:"search_key"`
	ResourceType string `json:"resource_type"`
	ExpectedValue string `json:"expected_value"`
	ActualValue   string `json:"actual_value"`
}

func parseKICSOutput(data []byte) ([]models.Finding, error) {
	var out kicsOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &out); err != nil {
		return nil, fmt.Errorf("kics: parse: %w", err)
	}
	var findings []models.Finding
	for _, q := range out.Queries {
		sev := mapKICSSeverity(q.Severity)
		for _, h := range q.Files {
			desc := q.Description
			if h.ExpectedValue != "" || h.ActualValue != "" {
				desc += fmt.Sprintf(" (expected=%s, actual=%s)", h.ExpectedValue, h.ActualValue)
			}
			findings = append(findings, models.Finding{
				ToolName:    "kics",
				Category:    models.CategoryInfra,
				Severity:    sev,
				Title:       fmt.Sprintf("[%s/%s] %s", q.Platform, q.Category, q.QueryName),
				Description: desc,
				FilePath:    h.FileName,
				LineStart:   h.Line,
				LineEnd:     h.Line,
				RuleID:      q.QueryID,
				CWEID:       cweOrEmpty(q.CWE),
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapKICSSeverity(s string) models.Severity {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return models.SeverityCritical
	case "HIGH":
		return models.SeverityHigh
	case "MEDIUM":
		return models.SeverityMedium
	case "LOW":
		return models.SeverityLow
	case "INFO", "TRACE":
		return models.SeverityInfo
	default:
		return models.SeverityInfo
	}
}

func cweOrEmpty(s string) string {
	if s == "" || s == "0" {
		return ""
	}
	if strings.HasPrefix(s, "CWE-") {
		return s
	}
	return "CWE-" + s
}
