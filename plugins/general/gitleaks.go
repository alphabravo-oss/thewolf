package general

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// GitleaksPlugin runs Gitleaks secret detection.
type GitleaksPlugin struct{}

func init() {
	plugin.Register(&GitleaksPlugin{})
}

func (p *GitleaksPlugin) Name() string               { return "gitleaks" }
func (p *GitleaksPlugin) Category() models.Category   { return models.CategorySecrets }
func (p *GitleaksPlugin) Languages() []models.Language { return nil }

func (p *GitleaksPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("gitleaks")
	return err == nil
}

func (p *GitleaksPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Write to a temp file instead of /dev/stdout (which may not be writable in sandboxed environments).
	reportFile := filepath.Join(os.TempDir(), fmt.Sprintf("gitleaks-%d.json", os.Getpid()))
	defer os.Remove(reportFile)

	args := []string{"detect", "--source", opts.RepoPath, "--report-format", "json", "--report-path", reportFile, "--no-git"}
	cmd := exec.CommandContext(ctx, "gitleaks", args...)
	cmd.Stderr = nil
	// Gitleaks exits with code 1 when leaks are found — that's not an error for us.
	_ = cmd.Run()

	out, err := os.ReadFile(reportFile)
	if err != nil {
		// No report file means gitleaks didn't produce output (e.g. no leaks found).
		return nil, nil
	}

	return parseGitleaksOutput(out)
}

type gitleaksFinding struct {
	Description string `json:"Description"`
	File        string `json:"File"`
	StartLine   int    `json:"StartLine"`
	EndLine     int    `json:"EndLine"`
	Match       string `json:"Match"`
	RuleID      string `json:"RuleID"`
	Entropy     float64 `json:"Entropy"`
	Secret      string `json:"Secret"`
}

func parseGitleaksOutput(data []byte) ([]models.Finding, error) {
	var results []gitleaksFinding
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse gitleaks output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, models.Finding{
			ToolName:    "gitleaks",
			Category:    models.CategorySecrets,
			Severity:    models.SeverityHigh,
			Title:       fmt.Sprintf("Secret detected: %s", r.RuleID),
			Description: r.Description,
			FilePath:    r.File,
			LineStart:   r.StartLine,
			LineEnd:     r.EndLine,
			CodeSnippet: r.Match,
			RuleID:      r.RuleID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
