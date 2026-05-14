package additional

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

// YamllintPlugin runs yamllint against the repo's YAML files. Uses the
// "parsable" format which emits one finding per line:
//
//	/scan/path/file.yaml:LINE:COL: [SEVERITY] message (rule)
type YamllintPlugin struct{}

func init() { plugin.Register(&YamllintPlugin{}) }

func (p *YamllintPlugin) Name() string                 { return "yamllint" }
func (p *YamllintPlugin) Category() models.Category    { return models.CategoryQuality }
func (p *YamllintPlugin) Languages() []models.Language { return nil }

func (p *YamllintPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *YamllintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "yaml", "yml") {
		plugin.Skipf(opts.OnOutput, "yamllint", "no YAML files (*.yaml, *.yml) found.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath},
		"yamllint", "--format", "parsable", "/scan")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("yamllint", err)
	}

	findings := parseYamllintOutput(out)
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

var yamllintLine = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+\[(error|warning|info)\]\s+(.+?)(?:\s+\(([^)]+)\))?\s*$`)

func parseYamllintOutput(data []byte) []models.Finding {
	var findings []models.Finding
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m := yamllintLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		rule := m[6]
		if rule == "" {
			rule = "yaml-syntax"
		}
		findings = append(findings, models.Finding{
			ToolName:    "yamllint",
			Category:    models.CategoryQuality,
			Severity:    mapYamllintSeverity(m[4]),
			Title:       fmt.Sprintf("[%s] %s", rule, m[5]),
			Description: m[5],
			FilePath:    m[1],
			LineStart:   lineNum,
			RuleID:      rule,
			Status:      models.StatusOpen,
		})
	}
	return findings
}

func mapYamllintSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityMedium
	case "warning":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
