package security

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// DetectSecretsPlugin runs Yelp's detect-secrets baseline scanner.
type DetectSecretsPlugin struct{}

func init() {
	plugin.Register(&DetectSecretsPlugin{})
}

func (p *DetectSecretsPlugin) Name() string             { return "detect-secrets" }
func (p *DetectSecretsPlugin) Category() models.Category { return models.CategorySecrets }
func (p *DetectSecretsPlugin) Languages() []models.Language { return nil }

func (p *DetectSecretsPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *DetectSecretsPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"detect-secrets", "scan", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("detect-secrets", err)
	}

	findings, perr := parseDetectSecretsOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type detectSecretsOutput struct {
	Results map[string][]detectSecretsResult `json:"results"`
}

type detectSecretsResult struct {
	Type         string `json:"type"`
	LineNumber   int    `json:"line_number"`
	HashedSecret string `json:"hashed_secret"`
}

// detectSecretsNoiseTypes lists detect-secrets detector types whose
// signal-to-noise is too poor to surface by default. KeywordDetector
// (Type="Secret Keyword") flags every line that *mentions* "secret",
// "password", "key", "token" as a substring — comments, variable
// names, docstrings, test fixtures. Yelp themselves document it as
// best-treated-as-baseline-only. We drop it; the other detectors
// (AWS, GitHub, AzureStorage, BasicAuth with credentials, high-
// entropy strings) are pattern-based and stay enabled.
var detectSecretsNoiseTypes = map[string]bool{
	"Secret Keyword": true,
}

func parseDetectSecretsOutput(data []byte) ([]models.Finding, error) {
	var output detectSecretsOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse detect-secrets output: %w", err)
	}

	var findings []models.Finding
	for file, secrets := range output.Results {
		for i, s := range secrets {
			if detectSecretsNoiseTypes[s.Type] {
				continue
			}
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
