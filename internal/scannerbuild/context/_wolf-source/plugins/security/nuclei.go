package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// NucleiPlugin runs Nuclei template-based vulnerability scanning.
type NucleiPlugin struct{}

func init() {
	plugin.Register(&NucleiPlugin{})
}

func (p *NucleiPlugin) Name() string                 { return "nuclei" }
func (p *NucleiPlugin) Category() models.Category    { return models.CategoryDAST }
func (p *NucleiPlugin) Languages() []models.Language { return nil }

func (p *NucleiPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *NucleiPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// Nuclei is a DAST scanner — it needs HTTP targets, not source code.
	// Without an explicit Target (URL/host) we have nothing to scan and the
	// previous fallback (`-l /scan` against the repo root) caused multi-hour
	// hangs because nuclei recursively walks the tree probing every file
	// as a URL. Skip cleanly so the scan doesn't get held up.
	if opts.Target == "" {
		plugin.Skipf(opts.OnOutput, "nuclei", "no DAST target provided; nuclei is a HTTP scanner and requires --target. Skipping.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Nuclei is a DAST scanner — it scans HTTP targets, not local repos.
	// When a target URL is provided, no repo mount is needed.
	// nuclei writes config + template cache to ~/.config/nuclei. As
	// non-root with HOME=/ that path is read-only; redirect with HOME=/tmp
	// and explicit -config-directory so the cache lives in the tmpfs.
	args := []string{
		"-jsonl", "-silent",
		"-config-directory", "/tmp/nuclei",
		"-u", opts.Target,
	}
	opt := container.Options{
		NoRepoMount: true,
		ExtraEnv:    map[string]string{"HOME": "/tmp"},
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		opt,
		"nuclei", args...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("nuclei", err)
	}

	findings, perr := parseNucleiOutputWithMetrics(out, opts.OnParseError)
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
	return parseNucleiOutputWithMetrics(data, nil)
}

func parseNucleiOutputWithMetrics(data []byte, onParseError func(error)) ([]models.Finding, error) {
	var findings []models.Finding

	// NDJSON — one bounded result per line. Advancing by line also guarantees
	// malformed records cannot trap the parser in a retry loop.
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		record := bytes.TrimSpace(scanner.Bytes())
		if len(record) == 0 {
			continue
		}
		var r nucleiResult
		if err := json.Unmarshal(record, &r); err != nil {
			notifyParseError(onParseError, err)
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
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan nuclei NDJSON: %w", err)
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
