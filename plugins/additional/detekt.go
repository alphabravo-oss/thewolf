package additional

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// DetektPlugin runs detekt for Kotlin static analysis. Detekt produces
// Checkstyle-style XML output which we parse here. Fills the Kotlin gap
// left by the Java-focused infer / pmd plugins.
type DetektPlugin struct{}

func init() { plugin.Register(&DetektPlugin{}) }

func (p *DetektPlugin) Name() string                 { return "detekt" }
func (p *DetektPlugin) Category() models.Category    { return models.CategorySAST }
func (p *DetektPlugin) Languages() []models.Language { return []models.Language{models.LangKotlin} }

func (p *DetektPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *DetektPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "kt", "kts") {
		plugin.Skipf(opts.OnOutput, "detekt", "no Kotlin files (*.kt, *.kts) found.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	if !cfg.HasDedicatedImage("detekt") {
		plugin.Skipf(opts.OnOutput, "detekt", "detekt requires the wolf-scanners-jvm bucket image; skipping.")
		return nil, nil
	}
	// detekt writes the report to a path; we use sh -c to read it back.
	script := "detekt --input /scan --report xml:/tmp/detekt.xml >/dev/null 2>&1 || true; " +
		"cat /tmp/detekt.xml 2>/dev/null || echo '<checkstyle/>'"
	// EntrypointOverride="sh" — the runner skips the tool-name dispatcher
	// when the override is set, so the args go straight to /bin/sh.
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir:            opts.RepoPath,
			EntrypointOverride: "sh",
		},
		"detekt", "-c", script)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("detekt", err)
	}

	findings, perr := parseDetektOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// Checkstyle XML schema (subset used by detekt).
type detektCheckstyle struct {
	XMLName xml.Name     `xml:"checkstyle"`
	Files   []detektFile `xml:"file"`
}

type detektFile struct {
	Name   string        `xml:"name,attr"`
	Errors []detektError `xml:"error"`
}

type detektError struct {
	Line     int    `xml:"line,attr"`
	Column   int    `xml:"column,attr"`
	Severity string `xml:"severity,attr"`
	Message  string `xml:"message,attr"`
	Source   string `xml:"source,attr"`
}

func parseDetektOutput(data []byte) ([]models.Finding, error) {
	var cs detektCheckstyle
	if err := xml.Unmarshal(plugin.ExtractXML(data), &cs); err != nil {
		return nil, fmt.Errorf("detekt: parse: %w", err)
	}
	var findings []models.Finding
	for _, f := range cs.Files {
		for _, e := range f.Errors {
			rule := e.Source
			rule = strings.TrimPrefix(rule, "detekt.")
			findings = append(findings, models.Finding{
				ToolName:    "detekt",
				Category:    models.CategorySAST,
				Severity:    mapDetektSeverity(e.Severity),
				Title:       rule,
				Description: e.Message,
				FilePath:    f.Name,
				LineStart:   e.Line,
				RuleID:      rule,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapDetektSeverity(s string) models.Severity {
	switch strings.ToLower(s) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "info":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
