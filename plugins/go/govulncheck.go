package goplug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// GovulncheckPlugin runs the official Go vulnerability scanner.
type GovulncheckPlugin struct{}

func init() {
	plugin.Register(&GovulncheckPlugin{})
}

func (p *GovulncheckPlugin) Name() string             { return "govulncheck" }
func (p *GovulncheckPlugin) Category() models.Category { return models.CategorySCA }
func (p *GovulncheckPlugin) Languages() []models.Language {
	return []models.Language{models.LangGo}
}

func (p *GovulncheckPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *GovulncheckPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "govulncheck", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// govulncheck needs the Go toolchain to resolve modules and a
	// writable HOME for the module cache.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, goDir),
			ExtraEnv: map[string]string{
				"HOME": "/tmp",
				"PATH": "/usr/local/go-toolchain/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			},
		},
		"govulncheck", "-json", "./...")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("govulncheck", err)
	}

	findings, perr := parseGovulncheckOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type govulnMessage struct {
	Finding *govulnFinding `json:"finding,omitempty"`
	Vuln    *govulnOSV     `json:"osv,omitempty"`
}

type govulnFinding struct {
	OSV   string          `json:"osv"`
	Trace []govulnFrame   `json:"trace"`
}

type govulnFrame struct {
	Module   string `json:"module,omitempty"`
	Package  string `json:"package,omitempty"`
	Function string `json:"function,omitempty"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position,omitempty"`
}

type govulnOSV struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
}

func parseGovulncheckOutput(data []byte) ([]models.Finding, error) {
	// govulncheck outputs newline-delimited JSON messages
	var findings []models.Finding
	osvMap := make(map[string]*govulnOSV)

	// Parse all messages
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var msg govulnMessage
		if err := dec.Decode(&msg); err != nil {
			continue
		}
		if msg.Vuln != nil {
			osvMap[msg.Vuln.ID] = msg.Vuln
		}
		if msg.Finding != nil {
			f := msg.Finding
			osv := osvMap[f.OSV]
			title := f.OSV
			desc := ""
			if osv != nil {
				title = fmt.Sprintf("%s: %s", f.OSV, osv.Summary)
				desc = osv.Details
			}

			file := ""
			line := 0
			if len(f.Trace) > 0 && f.Trace[0].Position != nil {
				file = f.Trace[0].Position.Filename
				line = f.Trace[0].Position.Line
			}

			findings = append(findings, models.Finding{
				ToolName:    "govulncheck",
				Category:    models.CategorySCA,
				Severity:    models.SeverityHigh,
				Title:       title,
				Description: desc,
				FilePath:    file,
				LineStart:   line,
				RuleID:      f.OSV,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

