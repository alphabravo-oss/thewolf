package python

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// VulturePlugin runs Vulture dead code finder for Python.
type VulturePlugin struct{}

func init() {
	plugin.Register(&VulturePlugin{})
}

func (p *VulturePlugin) Name() string               { return "vulture" }
func (p *VulturePlugin) Category() models.Category   { return models.CategoryQuality }
func (p *VulturePlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *VulturePlugin) CheckAvailable() bool { return container.IsScannersReady() }

var vultureLineRegex = regexp.MustCompile(`^(.+?):(\d+): (.+?) \((\d+)% confidence\)$`)

func (p *VulturePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "py") {
		plugin.Skipf(opts.OnOutput, "vulture", "no Python files (*.py) found. Add Python source files to enable dead code detection.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{RepoDir: opts.RepoPath},
		"vulture", "/scan", "--min-confidence", "80")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("vulture", err)
	}

	findings, perr := parseVultureOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

func parseVultureOutput(data []byte) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		line := scanner.Text()
		matches := vultureLineRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		filePath := matches[1]
		lineNum, _ := strconv.Atoi(matches[2])
		message := matches[3]
		confidence, _ := strconv.Atoi(matches[4])

		findings = append(findings, models.Finding{
			ToolName:    "vulture",
			Category:    models.CategoryQuality,
			Severity:    mapVultureSeverity(confidence),
			Title:       message,
			Description: fmt.Sprintf("%s (%d%% confidence)", message, confidence),
			FilePath:    filePath,
			LineStart:   lineNum,
			LineEnd:     lineNum,
			Status:      models.StatusOpen,
		})
	}

	return findings, scanner.Err()
}

func mapVultureSeverity(confidence int) models.Severity {
	if confidence >= 90 {
		return models.SeverityMedium
	}
	return models.SeverityLow
}
