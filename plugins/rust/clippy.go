package rust

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// ClippyPlugin runs cargo clippy for Rust code quality analysis.
type ClippyPlugin struct{}

func init() {
	plugin.Register(&ClippyPlugin{})
}

func (p *ClippyPlugin) Name() string              { return "clippy" }
func (p *ClippyPlugin) Category() models.Category { return models.CategoryQuality }
func (p *ClippyPlugin) Languages() []models.Language {
	return []models.Language{models.LangRust}
}

func (p *ClippyPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *ClippyPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// Verify this is a Rust project before attempting to run clippy.
	if _, err := os.Stat(filepath.Join(opts.RepoPath, "Cargo.toml")); err != nil {
		plugin.Skipf(opts.OnOutput, "clippy", "no Cargo.toml found — this doesn't appear to be a Rust project. Initialize with 'cargo init' to enable Clippy analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// clippy lives in the rust bucket image; the shim picks via ImageFor("clippy").
	// cargo needs to write to target/, so we use --read-only=false (writable /scan)
	// — actually cargo can target out-of-tree via CARGO_TARGET_DIR. Keep /scan
	// read-only and route target into /tmp via env.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: "/scan",
			ExtraEnv: map[string]string{
				"CARGO_TARGET_DIR": "/tmp/cargo-target",
				"CARGO_HOME":       "/tmp/cargo-home",
			},
		},
		"cargo", "clippy", "--message-format", "json")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("clippy", err)
	}

	findings, perr := parseClippyOutputWithMetrics(out, opts.OnParseError)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type clippyLine struct {
	Reason  string     `json:"reason"`
	Message *clippyMsg `json:"message"`
}

type clippyMsg struct {
	Level   string       `json:"level"`
	Message string       `json:"message"`
	Code    *clippyCode  `json:"code"`
	Spans   []clippySpan `json:"spans"`
}

type clippyCode struct {
	Code string `json:"code"`
}

type clippySpan struct {
	FileName  string       `json:"file_name"`
	LineStart int          `json:"line_start"`
	LineEnd   int          `json:"line_end"`
	Text      []clippyText `json:"text"`
}

type clippyText struct {
	Text string `json:"text"`
}

func parseClippyOutput(data []byte) ([]models.Finding, error) {
	return parseClippyOutputWithMetrics(data, nil)
}

func parseClippyOutputWithMetrics(data []byte, onParseError func(error)) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry clippyLine
		if err := json.Unmarshal(line, &entry); err != nil {
			notifyParseError(onParseError, err)
			continue
		}

		if entry.Reason != "compiler-message" || entry.Message == nil || entry.Message.Code == nil {
			continue
		}

		msg := entry.Message
		var filePath string
		var lineStart, lineEnd int
		var codeSnippet string

		if len(msg.Spans) > 0 {
			span := msg.Spans[0]
			filePath = span.FileName
			lineStart = span.LineStart
			lineEnd = span.LineEnd
			var texts []string
			for _, t := range span.Text {
				texts = append(texts, t.Text)
			}
			codeSnippet = strings.Join(texts, "\n")
		}

		findings = append(findings, models.Finding{
			ToolName:    "clippy",
			Category:    models.CategoryQuality,
			Severity:    mapClippySeverity(msg.Level),
			Title:       msg.Code.Code,
			Description: msg.Message,
			FilePath:    filePath,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			CodeSnippet: codeSnippet,
			RuleID:      msg.Code.Code,
			Status:      models.StatusOpen,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan clippy output: %w", err)
	}
	return findings, nil
}

func mapClippySeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	default:
		return models.SeverityInfo
	}
}
