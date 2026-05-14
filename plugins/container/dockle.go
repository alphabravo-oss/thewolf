package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	pkgcontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// DocklePlugin runs Dockle container best practice checks.
//
// Note: dockle is unusual among scanner plugins because it builds and inspects
// a docker IMAGE rather than scanning a repo directory. v1 keeps the build
// step at the wolf-slim level (which has access to the docker daemon socket
// already mounted in for the scanner orchestration) and runs dockle inside
// a scanner container that mounts the same docker socket so it can pull the
// just-built image from the local daemon.
//
// This is the ONE place where a scanner container needs docker socket access.
// We document the trade-off; if it becomes a concern, v2 can move dockle to
// wolf-fixer (which already has socket access for git push, etc.).
type DocklePlugin struct{}

func init() {
	plugin.Register(&DocklePlugin{})
}

func (p *DocklePlugin) Name() string                 { return "dockle" }
func (p *DocklePlugin) Category() models.Category    { return models.CategoryContainer }
func (p *DocklePlugin) Languages() []models.Language { return nil }

func (p *DocklePlugin) CheckAvailable() bool { return pkgcontainer.IsScannersReady() }

func (p *DocklePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	dockerfile := filepath.Join(opts.RepoPath, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		plugin.Skipf(opts.OnOutput, "dockle", "no Dockerfile found in repository root. Add a Dockerfile to enable container image analysis.")
		return nil, nil
	}

	// Build the image using the docker CLI mounted into wolf-slim.
	imageName := fmt.Sprintf("wolf-dockle-scan:%d", os.Getpid())
	buildCmd := exec.CommandContext(ctx, "docker", "build", "-t", imageName, "-f", dockerfile, opts.RepoPath)
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker build failed: %s: %w", string(buildOut), err)
	}
	defer func() {
		_ = exec.Command("docker", "rmi", "-f", imageName).Run()
	}()

	// Run dockle inside the scanner image, with the docker socket mounted so
	// dockle can fetch the just-built image from the local daemon.
	cfg := pkgcontainer.ConfigFromOpts(opts.ContainerCfg)
	cmd := pkgcontainer.CommandContext(ctx, cfg,
		pkgcontainer.Options{
			NoRepoMount: true,
			ExtraMounts: []string{"/var/run/docker.sock:/var/run/docker.sock"},
		},
		"dockle", "--format", "json", imageName)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("dockle", err)
	}

	return parseDockleOutput(out)
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
