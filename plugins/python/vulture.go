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

// VulturePlugin runs Vulture dead code finder for Python.
type VulturePlugin struct{}

func init() {
	plugin.Register(&VulturePlugin{})
}

func (p *VulturePlugin) Name() string               { return "vulture" }
func (p *VulturePlugin) Category() models.Category   { return models.CategoryQuality }
func (p *VulturePlugin) Languages() []models.Language { return []models.Language{models.LangPython} }

func (p *VulturePlugin) CheckAvailable() bool {
	_, err := exec.LookPath("vulture")
	return err == nil
}

var vultureLineRegex = regexp.MustCompile(`^(.+?):(\d+): (.+?) \((\d+)% confidence\)$`)

func (p *VulturePlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "vulture", opts.RepoPath, "--min-confidence", "80")
	out, err := cmd.Output()
	if err != nil {
		// Vulture exits non-zero when dead code is found; only fail if no output.
		if len(out) == 0 {
			return nil, fmt.Errorf("vulture execution failed: %w", err)
		}
	}

	return parseVultureOutput(out)
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
