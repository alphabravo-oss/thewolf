package cpp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// InferPlugin runs Facebook/Meta Infer for C/C++/Java/ObjC analysis.
type InferPlugin struct{}

func init() {
	plugin.Register(&InferPlugin{})
}

func (p *InferPlugin) Name() string             { return "infer" }
func (p *InferPlugin) Category() models.Category { return models.CategorySAST }
func (p *InferPlugin) Languages() []models.Language {
	return []models.Language{models.LangC, models.LangCPP, models.LangJava, models.LangObjC}
}

func (p *InferPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *InferPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// Facebook only ships infer for Linux/x86_64 — there's no Linux
	// arm64 binary on any infer release. Skip cleanly on arm64 hosts
	// rather than fail when the binary isn't in the jvm bucket image.
	if plugin.IsArm64Host() {
		plugin.Skipf(opts.OnOutput, "infer",
			"no upstream Linux/arm64 binary — infer is unavailable on this host. Skipping.")
		return nil, nil
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// Infer ships in the JVM bucket image (with pmd). It's not in the
	// default wolf-scanners. Skip cleanly when not configured rather
	// than fail with "exit 127".
	if !cfg.HasDedicatedImage("infer") {
		plugin.Skipf(opts.OnOutput, "infer",
			"not configured. Infer lives in the JVM bucket image; run `make scanners-build-jvm` then set WOLF_SCANNERS_IMAGE_JVM=wolf-scanners-jvm:dev. Skipping.")
		return nil, nil
	}
	if !plugin.HasFile(opts.RepoPath, "Makefile") {
		plugin.Skipf(opts.OnOutput, "infer", "no Makefile found — Infer requires a build system. Add a Makefile to enable analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	// Infer needs ReadWrite to write the infer-out/ directory during the build,
	// and runs the build (make) so it needs network for dependency fetches and
	// access to compiler toolchains (bundled in wolf-scanners-jvm).
	runOpts := container.Options{
		RepoDir:   opts.RepoPath,
		WorkDir:   "/scan",
		ReadWrite: true,
	}
	runCmd := container.CommandContext(ctx, cfg, runOpts, "infer", "run", "--", "make")
	if err := runCmd.Run(); err != nil {
		return nil, plugin.WrapExecError("infer", err)
	}

	reportCmd := container.CommandContext(ctx, cfg, runOpts, "infer", "report", "--issues-json", "-")
	out, err := reportCmd.Output()
	if err != nil {
		return nil, plugin.WrapExecError("infer", err)
	}

	findings, perr := parseInferOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type inferIssue struct {
	BugType     string `json:"bug_type"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Procedure   string `json:"procedure"`
	Qualifier   string `json:"qualifier"`
	BugTypeHum  string `json:"bug_type_hum"`
}

func parseInferOutput(data []byte) ([]models.Finding, error) {
	var issues []inferIssue
	if err := json.Unmarshal(plugin.ExtractJSON(data), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse infer output: %w", err)
	}

	findings := make([]models.Finding, 0, len(issues))
	for _, issue := range issues {
		findings = append(findings, models.Finding{
			ToolName:    "infer",
			Category:    models.CategorySAST,
			Severity:    mapInferSeverity(issue.Severity),
			Title:       issue.BugTypeHum,
			Description: issue.Qualifier,
			FilePath:    issue.File,
			LineStart:   issue.Line,
			RuleID:      issue.BugType,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapInferSeverity(s string) models.Severity {
	switch s {
	case "ERROR":
		return models.SeverityHigh
	case "WARNING":
		return models.SeverityMedium
	case "INFO", "ADVICE":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
