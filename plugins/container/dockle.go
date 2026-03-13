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
)

// DocklePlugin runs Dockle container best practice checks.
type DocklePlugin struct{}

func init() {
	plugin.Register(&DocklePlugin{})
}

func (p *DocklePlugin) Name() string               { return "dockle" }
func (p *DocklePlugin) Category() models.Category   { return models.CategoryContainer }
func (p *DocklePlugin) Languages() []models.Language { return nil }

func (p *DocklePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("dockle")
	return err == nil
}

func (p *DocklePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Dockle scans Docker images, not directories. Build a temporary image from
	// a Dockerfile if one exists in the repo, otherwise skip.
	dockerfile := filepath.Join(opts.RepoPath, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		return nil, nil // No Dockerfile — nothing to scan.
	}

	// Build a temporary image for scanning.
	imageName := fmt.Sprintf("wolf-dockle-scan:%d", os.Getpid())
	buildCmd := exec.CommandContext(ctx, "docker", "build", "-t", imageName, "-f", dockerfile, opts.RepoPath)
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker build failed: %s: %w", string(buildOut), err)
	}
	defer func() {
		_ = exec.Command("docker", "rmi", "-f", imageName).Run()
	}()

	args := []string{"--format", "json", imageName}
	cmd := exec.CommandContext(ctx, "dockle", args...)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("dockle execution failed: %w", err)
		}
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
	if err := json.Unmarshal(data, &output); err != nil {
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
