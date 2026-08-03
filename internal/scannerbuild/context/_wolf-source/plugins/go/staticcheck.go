package goplug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// StaticcheckPlugin runs staticcheck for Go code quality analysis.
type StaticcheckPlugin struct{}

func init() {
	plugin.Register(&StaticcheckPlugin{})
}

func (p *StaticcheckPlugin) Name() string              { return "staticcheck" }
func (p *StaticcheckPlugin) Category() models.Category { return models.CategoryQuality }
func (p *StaticcheckPlugin) Languages() []models.Language {
	return []models.Language{models.LangGo}
}

func (p *StaticcheckPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *StaticcheckPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "staticcheck", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// staticcheck caches build artifacts under $XDG_CACHE_HOME (defaults
	// to ~/.cache/staticcheck) and falls back to $HOME/.cache when XDG
	// isn't set. Our --read-only root + HOME=/ blocks both. Redirect
	// HOME into the per-container tmpfs. Also export PATH so the Go
	// toolchain is reachable (staticcheck shells out to `go list`).
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, goDir),
			ExtraEnv: map[string]string{
				"HOME": "/tmp",
				"PATH": "/usr/local/go-toolchain/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				// GOTOOLCHAIN=local disables Go's auto-download of a
				// newer toolchain when go.mod's `go` directive is
				// ahead of the container's installed Go. Auto-download
				// writes into $GOTOOLDIR which is read-only under our
				// --read-only root and fails with 'permission denied',
				// killing the whole staticcheck run. With GOTOOLCHAIN
				// pinned to 'local', Go uses whatever's installed in
				// the container; if that's too old to parse the source,
				// the failure message is at least diagnosable.
				"GOTOOLCHAIN": "local",
				// -buildvcs=false disables Go's VCS-stamping of binaries.
				// Without it, `go list` shells out to git and fails with
				// "error obtaining VCS status: exit status 128" inside
				// the read-only scanner container (no writable .git, no
				// git binary inheriting the host config). That bubbles
				// up as a fake 'compile' finding with no file/line. The
				// error message Go prints literally says
				//   "Use -buildvcs=false to disable VCS stamping."
				// GOFLAGS propagates the flag to every nested go command
				// staticcheck runs.
				"GOFLAGS": "-buildvcs=false",
			},
		},
		"staticcheck", "-f", "json", "./...")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("staticcheck", err)
	}

	findings, perr := parseStaticcheckOutputWithMetrics(out, opts.OnParseError)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

type staticcheckDiag struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Location struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location"`
	End struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"end"`
	Message string `json:"message"`
}

func parseStaticcheckOutput(data []byte) ([]models.Finding, error) {
	return parseStaticcheckOutputWithMetrics(data, nil)
}

func parseStaticcheckOutputWithMetrics(data []byte, onParseError func(error)) ([]models.Finding, error) {
	var findings []models.Finding
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var diag staticcheckDiag
		if err := json.Unmarshal(line, &diag); err != nil {
			notifyParseError(onParseError, err)
			continue
		}

		// Drop meta-errors. staticcheck emits Code="compile" entries
		// when go itself can't parse / build the project (failed `go
		// list`, missing tools in container, VCS-stamping failure,
		// etc.). They have no file/line, and they're scan-environment
		// issues — not quality findings — so they shouldn't show up
		// in the findings table at all.
		if diag.Code == "compile" && diag.Location.File == "" {
			continue
		}

		findings = append(findings, models.Finding{
			ToolName:    "staticcheck",
			Category:    models.CategoryQuality,
			Severity:    mapStaticcheckSeverity(diag.Severity),
			Title:       diag.Code,
			Description: diag.Message,
			FilePath:    diag.Location.File,
			LineStart:   diag.Location.Line,
			LineEnd:     diag.End.Line,
			RuleID:      diag.Code,
			Status:      models.StatusOpen,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan staticcheck output: %w", err)
	}
	return findings, nil
}

func mapStaticcheckSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}
