package security

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// ZizmorPlugin runs zizmor against GitHub Actions / Dependabot / pre-commit
// inputs. Offline mode keeps the scan inside the bind-mounted tree.
type ZizmorPlugin struct{}

func init() { plugin.Register(&ZizmorPlugin{}) }

func (p *ZizmorPlugin) Name() string                 { return "zizmor" }
func (p *ZizmorPlugin) Category() models.Category    { return models.CategorySAST }
func (p *ZizmorPlugin) Languages() []models.Language { return nil }

func (p *ZizmorPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ZizmorPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasZizmorInputs(opts.RepoPath) {
		plugin.Skipf(opts.OnOutput, "zizmor", "no GitHub Actions, Dependabot, composite action, or pre-commit config found.")
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
		home = "/var/lib/wolf-db/zizmor"
	}
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir: opts.RepoPath,
			ExtraEnv: map[string]string{
				"HOME":           home,
				"ZIZMOR_OFFLINE": "1",
			},
		},
		"zizmor", "--format=sarif", "--offline", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("zizmor", err)
	}
	findings, perr := parseZizmorOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

func parseZizmorOutput(data []byte) ([]models.Finding, error) {
	return parseSARIFFindings("zizmor", models.CategorySAST, data)
}
