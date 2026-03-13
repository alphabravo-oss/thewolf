package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// DetectSecretsPlugin runs Yelp's detect-secrets baseline scanner.
type DetectSecretsPlugin struct{}

func init() {
	plugin.Register(&DetectSecretsPlugin{})
}

func (p *DetectSecretsPlugin) Name() string             { return "detect-secrets" }
func (p *DetectSecretsPlugin) Category() models.Category { return models.CategorySecrets }
func (p *DetectSecretsPlugin) Languages() []models.Language { return nil }

func (p *DetectSecretsPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("detect-secrets")
	return err == nil
}

func (p *DetectSecretsPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "detect-secrets", "scan", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("detect-secrets execution failed: %w", err)
		}
	}

	return parseDetectSecretsOutput(out)
}

type detectSecretsOutput struct {
	Results map[string][]detectSecretsResult `json:"results"`
}

type detectSecretsResult struct {
	Type         string `json:"type"`
	LineNumber   int    `json:"line_number"`
	HashedSecret string `json:"hashed_secret"`
}

func parseDetectSecretsOutput(data []byte) ([]models.Finding, error) {
	var output detectSecretsOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse detect-secrets output: %w", err)
	}

	var findings []models.Finding
	for file, secrets := range output.Results {
		for i, s := range secrets {
			findings = append(findings, models.Finding{
				ToolName:    "detect-secrets",
				Category:    models.CategorySecrets,
				Severity:    models.SeverityHigh,
				Title:       fmt.Sprintf("Potential %s detected", s.Type),
				Description: fmt.Sprintf("Possible secret of type %q found at line %d", s.Type, s.LineNumber),
				FilePath:    file,
				LineStart:   s.LineNumber,
				RuleID:      "detect-secrets-" + strconv.Itoa(i),
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
