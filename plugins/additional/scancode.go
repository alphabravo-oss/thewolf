package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// ScancodePlugin runs ScanCode license scanning.
type ScancodePlugin struct{}

func init() {
	plugin.Register(&ScancodePlugin{})
}

func (p *ScancodePlugin) Name() string               { return "scancode" }
func (p *ScancodePlugin) Category() models.Category   { return models.CategoryLicense }
func (p *ScancodePlugin) Languages() []models.Language { return nil }

func (p *ScancodePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("scancode")
	return err == nil
}

func (p *ScancodePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"--json-pp", "-", "--license", opts.RepoPath}
	cmd := exec.CommandContext(ctx, "scancode", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("scancode execution failed: %w", err)
		}
	}

	return parseScancodeOutput(out)
}

type scancodeOutput struct {
	Files []scancodeFile `json:"files"`
}

type scancodeFile struct {
	Path     string            `json:"path"`
	Licenses []scancodeLicense `json:"licenses"`
}

type scancodeLicense struct {
	Key            string  `json:"key"`
	Score          float64 `json:"score"`
	Name           string  `json:"name"`
	Category       string  `json:"category"`
	SPDXID         string  `json:"spdx_license_key"`
	StartLine      int     `json:"start_line"`
	EndLine        int     `json:"end_line"`
	MatchedText    string  `json:"matched_text"`
}

func parseScancodeOutput(data []byte) ([]models.Finding, error) {
	var output scancodeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse scancode output: %w", err)
	}

	var findings []models.Finding
	for _, f := range output.Files {
		for _, lic := range f.Licenses {
			sev := models.SeverityInfo
			if lic.Category == "Copyleft" || lic.Category == "Copyleft Limited" {
				sev = models.SeverityMedium
			}

			findings = append(findings, models.Finding{
				ToolName:    "scancode",
				Category:    models.CategoryLicense,
				Severity:    sev,
				Title:       fmt.Sprintf("License: %s (%s)", lic.Name, lic.SPDXID),
				Description: fmt.Sprintf("License %s (category: %s, score: %.0f%%)", lic.Name, lic.Category, lic.Score),
				FilePath:    f.Path,
				LineStart:   lic.StartLine,
				LineEnd:     lic.EndLine,
				CodeSnippet: lic.MatchedText,
				RuleID:      lic.Key,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
