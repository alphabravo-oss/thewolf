package security

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// DetectSecretsPlugin runs Yelp's detect-secrets baseline scanner.
type DetectSecretsPlugin struct{}

func init() {
	plugin.Register(&DetectSecretsPlugin{})
}

func (p *DetectSecretsPlugin) Name() string                 { return "detect-secrets" }
func (p *DetectSecretsPlugin) Category() models.Category    { return models.CategorySecrets }
func (p *DetectSecretsPlugin) Languages() []models.Language { return nil }

func (p *DetectSecretsPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *DetectSecretsPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	excluded := make([]string, len(plugin.DefaultExcludeDirs))
	for i, dir := range plugin.DefaultExcludeDirs {
		excluded[i] = regexp.QuoteMeta(dir)
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath, ExtraEnv: map[string]string{
			// Python otherwise forks for all host CPUs despite Docker's quota.
			"PYTHON_CPU_COUNT": strconv.Itoa(max(1, int(container.CPUCount(cfg)))),
		}},
		// Detection stays local; online credential verification would retry
		// against the network-isolated sandbox and stall on every candidate.
		// KeywordDetector findings are already discarded by our parser.
		// Archive snapshots may have no .git; do not silently scan zero files.
		"detect-secrets", "scan", "--all-files", "--no-verify", "--disable-plugin", "KeywordDetector",
		"--exclude-files", "(^|/)("+strings.Join(excluded, "|")+")(/|$)", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("detect-secrets did not finish: %w", ctx.Err())
		}
		return nil, plugin.WrapExecError("detect-secrets", err)
	}
	plugin.SaveRaw(opts, out, "json")

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

// detectSecretsEntropyTypes match any long random-looking string, including
// SHA-256 digests in lockfiles, manifests and evidence JSON. They stay visible
// at low severity; pattern-based detectors (AWS, Private Key, ...) stay high.
var detectSecretsEntropyTypes = map[string]bool{
	"Hex High Entropy String":    true,
	"Base64 High Entropy String": true,
}

func parseDetectSecretsOutput(data []byte) ([]models.Finding, error) {
	var output detectSecretsOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse detect-secrets output: %w", err)
	}

	var findings []models.Finding
	for file, secrets := range output.Results {
		for _, s := range secrets {
			if detectSecretsNoiseTypes[s.Type] {
				continue
			}
			id := s.HashedSecret
			if id == "" {
				id = strconv.Itoa(s.LineNumber)
			}
			severity := models.SeverityHigh
			if detectSecretsEntropyTypes[s.Type] {
				severity = models.SeverityLow
			}
			findings = append(findings, models.Finding{
				ToolName:    "detect-secrets",
				Category:    models.CategorySecrets,
				Severity:    severity,
				Title:       fmt.Sprintf("Potential %s detected", s.Type),
				Description: fmt.Sprintf("Possible secret of type %q found at line %d", s.Type, s.LineNumber),
				FilePath:    file,
				LineStart:   s.LineNumber,
				RuleID:      s.Type + ":" + id,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
