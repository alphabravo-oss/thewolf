package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	pkgcontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// DocklePlugin runs Dockle container best practice checks.
//
// Dockle is unusual because it inspects an image rather than a source tree.
// Wolf deliberately never mounts a container-engine socket into a scanner.
// A /scan/*.tar target is therefore passed to Dockle's supported --input
// archive mode. Other targets are treated as immutable/remote image
// references that Dockle resolves without access to the host daemon.
type DocklePlugin struct{}

func init() {
	plugin.Register(&DocklePlugin{})
}

func (p *DocklePlugin) Name() string                 { return "dockle" }
func (p *DocklePlugin) Category() models.Category    { return models.CategoryContainer }
func (p *DocklePlugin) Languages() []models.Language { return nil }

func (p *DocklePlugin) CheckAvailable() bool { return pkgcontainer.IsScannersReady() }

func (p *DocklePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// dockle scans **built container images**, not source. The previous
	// behavior of docker-building the repo's Dockerfile in-band was
	// brittle: any complex Dockerfile (multi-stage, host-tool deps, Go
	// version skew) silently failed the scan. Mirror the nuclei pattern:
	// require opts.Target to be a pre-built image name (or :tag) the
	// operator has already produced or pulled.
	if opts.Target == "" {
		plugin.Skipf(opts.OnOutput, "dockle",
			"no image target provided. dockle scans built images, not Dockerfiles; pass --target <image:tag> to enable. Skipping.")
		return nil, nil
	}
	// Defensive sanity-check: presence of a Dockerfile is informational
	// only — the user still has to build it themselves.
	if _, err := os.Stat(filepath.Join(opts.RepoPath, "Dockerfile")); err != nil {
		plugin.Infof(opts.OnOutput, "dockle", "no Dockerfile in repo root, but --target was provided; proceeding to scan %s", opts.Target)
	}

	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cfg := pkgcontainer.ConfigFromOpts(opts.ContainerCfg)
	commandOptions, arguments, err := dockleInvocation(opts.Target)
	if err != nil {
		return nil, err
	}
	cmd := pkgcontainer.CommandContext(ctx, cfg,
		commandOptions, "dockle", arguments...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("dockle", err)
	}
	plugin.SaveRaw(opts, out, "json")

	return parseDockleOutput(out)
}

func dockleInvocation(target string) (pkgcontainer.Options, []string, error) {
	clean := filepath.Clean(target)
	if strings.HasPrefix(clean, "/scan/") && strings.HasSuffix(clean, ".tar") {
		return pkgcontainer.Options{}, []string{"--format", "json", "--input", clean}, nil
	}
	if strings.HasPrefix(clean, "/") || strings.ContainsAny(target, "\x00\r\n") {
		return pkgcontainer.Options{}, nil, fmt.Errorf("dockle target must be a /scan/*.tar archive or an image reference")
	}
	return pkgcontainer.Options{NoRepoMount: true}, []string{"--format", "json", target}, nil
}

type dockleOutput struct {
	Details []dockleDetail `json:"details"`
}

type dockleDetail struct {
	Code   string   `json:"code"`
	Title  string   `json:"title"`
	Level  string   `json:"level"`
	Alerts []string `json:"alerts"`
}

func parseDockleOutput(data []byte) ([]models.Finding, error) {
	var output dockleOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse dockle output: %w", err)
	}

	var findings []models.Finding
	for _, d := range output.Details {
		desc := d.Title
		if len(d.Alerts) > 0 {
			desc += ": " + d.Alerts[0]
		}
		findings = append(findings, models.Finding{
			ToolName:    "dockle",
			Category:    models.CategoryContainer,
			Severity:    mapDockleSeverity(d.Level),
			Title:       fmt.Sprintf("[%s] %s", d.Code, d.Title),
			Description: desc,
			RuleID:      d.Code,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapDockleSeverity(level string) models.Severity {
	switch level {
	case "FATAL":
		return models.SeverityCritical
	case "WARN":
		return models.SeverityMedium
	case "INFO":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
