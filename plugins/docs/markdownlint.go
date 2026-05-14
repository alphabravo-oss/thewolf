package docs

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

// MarkdownlintPlugin runs markdownlint-cli against the repo's Markdown
// files. Output is text on stderr in the form:
//
//	/scan/path/file.md:LINE:COL MDxxx/rule-name Description [Context]
//
// One finding per line.
type MarkdownlintPlugin struct{}

func init() { plugin.Register(&MarkdownlintPlugin{}) }

func (p *MarkdownlintPlugin) Name() string                 { return "markdownlint" }
func (p *MarkdownlintPlugin) Category() models.Category    { return models.CategoryDocs }
func (p *MarkdownlintPlugin) Languages() []models.Language { return nil }

func (p *MarkdownlintPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *MarkdownlintPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "md", "markdown") {
		plugin.Skipf(opts.OnOutput, "markdownlint", "no Markdown files found.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// markdownlint writes its findings on stderr, exits non-zero when any
	// finding is present. We redirect stderr→stdout and ignore exit code.
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir:            opts.RepoPath,
			EntrypointOverride: "sh",
		},
		"sh", "-c", "markdownlint '/scan/**/*.md' 2>&1 || true")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("markdownlint", err)
	}

	findings := parseMarkdownlintOutput(out)
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

var mdlintLine = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?\s+(MD\d+)(?:/([^\s]+))?\s+(.+?)(?:\s+\[(.+)\])?$`)

func parseMarkdownlintOutput(data []byte) []models.Finding {
	var findings []models.Finding
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m := mdlintLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		findings = append(findings, models.Finding{
			ToolName:    "markdownlint",
			Category:    models.CategoryDocs,
			Severity:    models.SeverityLow,
			Title:       fmt.Sprintf("[%s] %s", m[4], m[6]),
			Description: m[6],
			FilePath:    m[1],
			LineStart:   lineNum,
			RuleID:      m[4],
			Status:      models.StatusOpen,
		})
	}
	return findings
}
