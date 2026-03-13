package general

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// TrufflehogPlugin runs TruffleHog secret detection.
type TrufflehogPlugin struct{}

func init() {
	plugin.Register(&TrufflehogPlugin{})
}

func (p *TrufflehogPlugin) Name() string               { return "trufflehog" }
func (p *TrufflehogPlugin) Category() models.Category   { return models.CategorySecrets }
func (p *TrufflehogPlugin) Languages() []models.Language { return nil }

func (p *TrufflehogPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("trufflehog")
	return err == nil
}

func (p *TrufflehogPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"filesystem", "--json", opts.RepoPath}
	cmd := exec.CommandContext(ctx, "trufflehog", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("trufflehog execution failed: %w", err)
		}
	}

	return parseTrufflehogOutput(out)
}

type trufflehogResult struct {
	DetectorName string `json:"DetectorName"`
	Verified     bool   `json:"Verified"`
	Raw          string `json:"Raw"`
	SourceMetadata struct {
		Data struct {
			Filesystem struct {
				File string `json:"file"`
				Line int    `json:"line"`
			} `json:"Filesystem"`
		} `json:"Data"`
	} `json:"SourceMetadata"`
}

func parseTrufflehogOutput(data []byte) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r trufflehogResult
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}

		sev := models.SeverityMedium
		if r.Verified {
			sev = models.SeverityCritical
		}

		findings = append(findings, models.Finding{
			ToolName:    "trufflehog",
			Category:    models.CategorySecrets,
			Severity:    sev,
			Title:       fmt.Sprintf("Secret detected: %s", r.DetectorName),
			Description: fmt.Sprintf("Detected %s secret (verified: %t)", r.DetectorName, r.Verified),
			FilePath:    r.SourceMetadata.Data.Filesystem.File,
			LineStart:   r.SourceMetadata.Data.Filesystem.Line,
			RuleID:      r.DetectorName,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}
