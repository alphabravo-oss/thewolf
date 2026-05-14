package python

import (
	"bufio"
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// MypyPlugin runs mypy type checker for Python.
type MypyPlugin struct{}

func init() {
	plugin.Register(&MypyPlugin{})
}

func (p *MypyPlugin) Name() string               { return "mypy" }
func (p *MypyPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *MypyPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *MypyPlugin) CheckAvailable() bool { return container.IsScannersReady() }

var mypyLineRegex = regexp.MustCompile(`^(.+?):(\d+):(\d+): (error|warning|note): (.+?)(?:\s+\[(.+?)\])?$`)

func (p *MypyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "py") {
		plugin.Skipf(opts.OnOutput, "mypy", "no Python files (*.py) found. Add Python source files to enable type checking.")
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
		"mypy", "/scan",
		"--no-error-summary", "--show-column-numbers", "--show-error-codes", "--no-color")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("mypy", err)
	}

	findings, perr := parseMypyOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

func parseMypyOutput(data []byte) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		line := scanner.Text()
		matches := mypyLineRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		filePath := matches[1]
		lineNum, _ := strconv.Atoi(matches[2])
		level := matches[4]
		message := matches[5]
		errorCode := matches[6]

		findings = append(findings, models.Finding{
			ToolName:    "mypy",
			Category:    models.CategoryQuality,
			Severity:    mapMypySeverity(level),
			Title:       message,
			Description: line,
			FilePath:    filePath,
			LineStart:   lineNum,
			LineEnd:     lineNum,
			RuleID:      errorCode,
			Status:      models.StatusOpen,
		})
	}

	return findings, scanner.Err()
}

func mapMypySeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
