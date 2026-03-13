package python

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// MypyPlugin runs mypy type checker for Python.
type MypyPlugin struct{}

func init() {
	plugin.Register(&MypyPlugin{})
}

func (p *MypyPlugin) Name() string               { return "mypy" }
func (p *MypyPlugin) Category() models.Category   { return models.CategoryQuality }
func (p *MypyPlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *MypyPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("mypy")
	return err == nil
}

var mypyLineRegex = regexp.MustCompile(`^(.+?):(\d+):(\d+): (error|warning|note): (.+?)(?:\s+\[(.+?)\])?$`)

func (p *MypyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "mypy", opts.RepoPath,
		"--no-error-summary", "--show-column-numbers", "--show-error-codes", "--no-color")
	out, err := cmd.Output()
	if err != nil {
		// mypy exits non-zero when type errors are found; only fail if no output.
		if len(out) == 0 {
			return nil, fmt.Errorf("mypy execution failed: %w", err)
		}
	}

	return parseMypyOutput(out)
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
