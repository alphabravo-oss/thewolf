package general

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// SemgrepPlugin runs Semgrep static analysis.
type SemgrepPlugin struct{}

func init() {
	plugin.Register(&SemgrepPlugin{})
}

func (p *SemgrepPlugin) Name() string                 { return "semgrep" }
func (p *SemgrepPlugin) Category() models.Category    { return models.CategorySAST }
func (p *SemgrepPlugin) Languages() []models.Language { return nil }

func (p *SemgrepPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *SemgrepPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	jobs := max(1, int(container.CPUCount(cfg)))
	args := []string{"--jobs", strconv.Itoa(jobs), "--disable-version-check"}
	args = append(args, plugin.ExcludeArgs("--exclude")...)
	args = append(args, "/scan")
	// semgrep writes first-run settings and registry rule caches to
	// ~/.semgrep. The image HOME is read-only under our mount. Persist
	// on the shared wolf-db volume so later scans do not re-fetch
	// p/ci (and similar) from the registry.
	home := "/tmp"
	if cfg != nil && cfg.DBVolume != "" {
		home = "/var/lib/wolf-db/semgrep"
	}
	cmd := container.CommandContext(ctx,
		cfg,
		container.Options{
			RepoDir:            opts.RepoPath,
			ExtraEnv:           map[string]string{"HOME": home},
			EntrypointOverride: "sh",
		},
		"semgrep", append([]string{"-c", semgrepRulesCommand, "wolf-semgrep", container.ResolveNetwork(cfg, "semgrep")}, args...)...)

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
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var diagnostic string
	for scanner.Scan() {
		line := scanner.Text()
		diagnostic += line + "\n"
		if len(diagnostic) > 4096 {
			diagnostic = diagnostic[len(diagnostic)-4096:]
		}
		if opts.OnOutput != nil {
			opts.OnOutput(line)
		}
	}

	readErr := scanner.Err()
	if readErr != nil {
		_ = stderr.Close()
	}
	runErr := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitErr.Stderr = []byte(diagnostic)
	}
	if runErr != nil {
		runErr = plugin.WrapExecError("semgrep", runErr)
	}

	out := stdout.Bytes()
	if len(out) == 0 {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("semgrep produced no output")
	}
	plugin.SaveRaw(opts, out, "json")

	findings, perr := parseSemgrepOutput(out)
	if perr != nil {
		return nil, errors.Join(runErr, perr)
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	var result semgrepOutput
	if json.Unmarshal(plugin.ExtractJSON(out), &result) == nil {
		for _, issue := range result.Errors {
			if opts.OnParseError != nil {
				opts.OnParseError(fmt.Errorf("semgrep: %s", strings.SplitN(issue.Message, "\n", 2)[0]))
			}
		}
		if len(result.Errors) > 0 {
			runErr = errors.Join(runErr, fmt.Errorf("semgrep returned %d findings with %d analysis warnings/errors; coverage is incomplete; first: %s", len(findings), len(result.Errors), strings.SplitN(result.Errors[0].Message, "\n", 2)[0]))
		}
	}
	return findings, errors.Join(runErr, readErr)
}

// Prepared rules belong to the scanner image/cache, never the untrusted repo.
// Without prepared rules, fall back to the curated registry pack (needs network).
// Not --config auto: Semgrep refuses auto when metrics are off, which Wolf requires.
const semgrepRulesCommand = `network=$1; shift
rules=p/default
for candidate in /etc/wolf-scanners/semgrep.yml /etc/wolf-scanners/semgrep /var/lib/wolf-db/semgrep/rules.yaml /var/lib/wolf-db/semgrep/rules; do
  if [ -r "$candidate" ]; then rules=$candidate; break; fi
done
if [ "$rules" = p/default ] && [ "$network" = none ]; then
  echo 'needs network or database: Semgrep has no prepared rules. Install rules at /var/lib/wolf-db/semgrep/rules.yaml or enable scanner network access, then retry.' >&2
  exit 78
fi
exec semgrep scan --json --config "$rules" "$@"`

// semgrepOutput represents the JSON output from semgrep.
type semgrepOutput struct {
	Results []semgrepResult `json:"results"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
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
			CodeSnippet: semgrepSnippet(r.Extra.Lines),
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

// Semgrep without a login replaces source lines with this placeholder; it is not code.
func semgrepSnippet(lines string) string {
	if strings.TrimSpace(lines) == "requires login" {
		return ""
	}
	return lines
}
