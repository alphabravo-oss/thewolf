package general

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

// SemgrepPlugin runs Semgrep static analysis.
type SemgrepPlugin struct{}

func init() {
	plugin.Register(&SemgrepPlugin{})
}

func (p *SemgrepPlugin) Name() string               { return "semgrep" }
func (p *SemgrepPlugin) Category() models.Category   { return models.CategorySAST }
func (p *SemgrepPlugin) Languages() []models.Language { return nil }

func (p *SemgrepPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *SemgrepPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"scan", "--json", "--jobs", "1"}
	args = append(args, plugin.ExcludeArgs("--exclude")...)
	args = append(args, "/scan")
	// semgrep writes its first-run settings to ~/.semgrep. The image's
	// HOME is /home/semgrep which is read-only under our --read-only
	// mount, so the binary crashes on import. Redirect HOME to the
	// per-container tmpfs.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir:  opts.RepoPath,
			ExtraEnv: map[string]string{"HOME": "/tmp"},
		},
		"semgrep", args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("semgrep stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("semgrep start: %w", err)
	}

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if opts.OnOutput != nil {
			opts.OnOutput(line)
		}
	}

	_ = cmd.Wait()

	out := stdout.Bytes()
	if len(out) == 0 {
		return nil, fmt.Errorf("semgrep produced no output")
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseSemgrepOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// semgrepOutput represents the JSON output from semgrep.
type semgrepOutput struct {
	Results []semgrepResult `json:"results"`
}

type semgrepResult struct {
	CheckID string `json:"check_id"`
	Path    string `json:"path"`
	Start   struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	} `json:"start"`
	End struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	} `json:"end"`
	Extra struct {
		Message  string `json:"message"`
		Severity string `json:"severity"`
		Metadata struct {
			CWE        interface{} `json:"cwe"`
			Category   string      `json:"category"`
			Confidence string      `json:"confidence"`
		} `json:"metadata"`
		Lines string `json:"lines"`
	} `json:"extra"`
}

func parseSemgrepOutput(data []byte) ([]models.Finding, error) {
	var output semgrepOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse semgrep output: %w", err)
	}

	findings := make([]models.Finding, 0, len(output.Results))
	for _, r := range output.Results {
		findings = append(findings, models.Finding{
			ToolName:    "semgrep",
			Category:    models.CategorySAST,
			Severity:    mapSemgrepSeverity(r.Extra.Severity),
			Title:       r.CheckID,
			Description: r.Extra.Message,
			FilePath:    r.Path,
			LineStart:   r.Start.Line,
			LineEnd:     r.End.Line,
			CodeSnippet: r.Extra.Lines,
			RuleID:      r.CheckID,
			CWEID:       extractCWE(r.Extra.Metadata.CWE),
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapSemgrepSeverity(s string) models.Severity {
	switch s {
	case "ERROR":
		return models.SeverityHigh
	case "WARNING":
		return models.SeverityMedium
	case "INFO":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}

func extractCWE(cwe interface{}) string {
	switch v := cwe.(type) {
	case string:
		return v
	case []interface{}:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}
