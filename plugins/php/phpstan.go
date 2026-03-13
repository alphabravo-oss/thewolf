package php

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// PHPStanPlugin runs PHPStan static analysis for PHP code.
type PHPStanPlugin struct{}

func init() {
	plugin.Register(&PHPStanPlugin{})
}

func (p *PHPStanPlugin) Name() string             { return "phpstan" }
func (p *PHPStanPlugin) Category() models.Category { return models.CategorySAST }
func (p *PHPStanPlugin) Languages() []models.Language {
	return []models.Language{models.LangPHP}
}

func (p *PHPStanPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("phpstan")
	return err == nil
}

func (p *PHPStanPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "php") {
		plugin.Skipf(opts.OnOutput, "phpstan", "no PHP files (*.php) found. Add PHP source files to enable static analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "phpstan", "analyse", "--error-format=json", "--no-progress", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("phpstan", err)
		}
	}

	return parsePHPStanOutput(out)
}

type phpstanOutput struct {
	Files map[string]phpstanFile `json:"files"`
}

type phpstanFile struct {
	Errors   int              `json:"errors"`
	Messages []phpstanMessage `json:"messages"`
}

type phpstanMessage struct {
	Message   string `json:"message"`
	Line      int    `json:"line"`
	Ignorable bool   `json:"ignorable"`
}

func parsePHPStanOutput(data []byte) ([]models.Finding, error) {
	var output phpstanOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &output); err != nil {
		return nil, fmt.Errorf("failed to parse phpstan output: %w", err)
	}

	var findings []models.Finding
	for file, f := range output.Files {
		for _, msg := range f.Messages {
			findings = append(findings, models.Finding{
				ToolName:    "phpstan",
				Category:    models.CategorySAST,
				Severity:    models.SeverityMedium,
				Title:       msg.Message,
				Description: msg.Message,
				FilePath:    file,
				LineStart:   msg.Line,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}
