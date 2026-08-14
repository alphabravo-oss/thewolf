package security

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// PoutinePlugin runs poutine against local CI pipeline definitions.
type PoutinePlugin struct{}

func init() { plugin.Register(&PoutinePlugin{}) }

func (p *PoutinePlugin) Name() string                 { return "poutine" }
func (p *PoutinePlugin) Category() models.Category    { return models.CategorySAST }
func (p *PoutinePlugin) Languages() []models.Language { return nil }

func (p *PoutinePlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *PoutinePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasPipelineConfig(opts.RepoPath) {
		plugin.Skipf(opts.OnOutput, "poutine", "no GitHub Actions, GitLab CI, Azure Pipelines, or Tekton config found.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	home := "/tmp"
	if cfg.DBVolume != "" {
		home = "/var/lib/wolf-db/poutine"
	}
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir: opts.RepoPath,
			ExtraEnv: map[string]string{
				"HOME":                          home,
				"POUTINE_DISABLE_VERSION_CHECK": "1",
			},
		},
		"poutine", "analyze_local", "/scan",
		"--format", "sarif",
		"--disable-version-check")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("poutine", err)
	}
	findings, perr := parsePoutineOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

func parsePoutineOutput(data []byte) ([]models.Finding, error) {
	return parseSARIFFindings("poutine", models.CategorySAST, data)
}
