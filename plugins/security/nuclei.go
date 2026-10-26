package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/dast"
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

func (p *NucleiPlugin) Applicability(repoPath, target string) error {
	if target == "" {
		return &plugin.NotApplicableError{Reason: "no DAST target provided; Nuclei requires an HTTP target"}
	}
	return nil
}

func (p *NucleiPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// Repository scans run only against an administrator-approved target,
	// through the approved-origin proxy. Scanner-release qualification passes
	// a fixture Target with no DAST execution; it runs on its own controlled
	// wolf-quality network, so it connects directly.
	target := opts.Target
	var headers map[string]string
	if opts.DAST != nil {
		if !opts.DAST.Target.Approved() {
			return nil, &plugin.NotApplicableError{Reason: "no approved repository DAST target"}
		}
		target, headers = opts.DAST.Target.URL, opts.DAST.Headers
	}
	if err := p.Applicability(opts.RepoPath, target); err != nil {
		return nil, err
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	var proxy *dast.Proxy
	if opts.DAST != nil {
		var err error
		if proxy, err = dast.StartProxy(ctx, opts.DAST.Target); err != nil {
			return nil, err
		}
		defer proxy.Close()
	}
	headerLines := []string{}
	for name, value := range headers {
		headerLines = append(headerLines, name+": "+value)
	}
	config, err := json.Marshal(map[string]any{"header": headerLines})
	if err != nil {
		return nil, err
	}
	// HTTP templates only: network/headless/code templates cannot bypass the
	// approved-origin proxy. Disable out-of-band callbacks and update traffic.
	args := []string{"-c", `umask 077; cat > /tmp/wolf-nuclei.yaml; exec nuclei "$@"`, "nuclei",
		"-jsonl", "-silent", "-config", "/tmp/wolf-nuclei.yaml", "-omit-raw",
		"-config-directory", "/tmp/nuclei", "-u", target,
		"-pt", "http", "-ni", "-duc", "-dr",
	}
	if proxy != nil {
		args = append(args, "-proxy", proxy.URL)
	}
	opt := container.Options{NoRepoMount: true, DASTProxy: proxy != nil, EntrypointOverride: "sh", Stdin: string(config), ExtraEnv: map[string]string{"HOME": "/tmp"}}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		opt,
		"nuclei", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Nuclei failed to scan the target: %v", err)
	}
	if proxy != nil && !proxy.ReachedTarget() {
		return nil, fmt.Errorf("Nuclei could not reach the approved target (check target availability and installed templates)")
	}
	for _, secret := range headers {
		encoded, _ := json.Marshal(secret)
		if len(encoded) > 2 {
			out = bytes.ReplaceAll(out, encoded[1:len(encoded)-1], []byte("[REDACTED]"))
		}
	}
	plugin.SaveRaw(opts, out, "jsonl")

	return parseNucleiOutputWithMetrics(out, opts.OnParseError)
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
