package goplug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func bytesContain(data []byte, s string) bool { return bytes.Contains(data, []byte(s)) }

// GoKartPlugin runs Praetorian's GoKart, a source-to-sink taint-analysis
// SAST for Go. Complements gosec (pattern-based): gosec flags constructs
// that LOOK risky; GoKart traces user input through the program and only
// flags constructs that ACTUALLY receive tainted data.
//
// GoKart writes JSON to stdout via the `-r` / `-o` flag.
type GoKartPlugin struct{}

func init() { plugin.Register(&GoKartPlugin{}) }

func (p *GoKartPlugin) Name() string                 { return "gokart" }
func (p *GoKartPlugin) Category() models.Category    { return models.CategorySAST }
func (p *GoKartPlugin) Languages() []models.Language { return []models.Language{models.LangGo} }

func (p *GoKartPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *GoKartPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	goDir := plugin.FindFile(opts.RepoPath, "go.mod")
	if goDir == "" {
		plugin.Skipf(opts.OnOutput, "gokart", "no go.mod found in project or immediate subdirectories.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	// gokart writes a config file to ~/.gokart on first invocation;
	// HOME=/ is read-only under our --read-only mount so the write
	// fails. Redirect HOME into the per-container tmpfs.
	//
	// gokart also shells out to `go` for module resolution. The
	// wolf-scanners image installs the toolchain at
	// /usr/local/go-toolchain but doesn't add it to the runtime PATH,
	// so we prepend it here. Without this gokart resolves `go` to a
	// `./go` directory in the scanned repo and exits immediately.
	cmd := container.CommandContext(ctx,
		container.ConfigFromOpts(opts.ContainerCfg),
		container.Options{
			RepoDir: opts.RepoPath,
			WorkDir: container.ContainerSubPath(opts.RepoPath, goDir),
			ExtraEnv: map[string]string{
				"HOME":        "/tmp",
				"PATH":        "/usr/local/go-toolchain/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				"GOTOOLCHAIN": "local",
			},
		},
		"gokart", "scan", "-o", "json", "./...")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("gokart", err)
	}

	// gokart v0.5.x bundles golang.org/x/tools@v0.1.12, which can't read
	// the export-data format used by Go 1.23+ packages. The binary
	// panics during analysis ("unsupported version: 2") with the panic
	// trace going to stderr; stdout receives only the startup banner
	// ("Initializing default config ...", "Revving engines"). Detect
	// the banner-without-JSON case and skip gracefully rather than
	// surfacing a confusing "invalid character 'I'" parse error.
	// Upstream issue: praetorian-inc/gokart has not shipped a release
	// compatible with modern Go since 2023.
	if bytesContain(out, "Revving engines") || bytesContain(out, "Initializing default config") {
		// Only skip if there's no JSON payload appended after the banner.
		if !bytesContain(out, "{") && !bytesContain(out, "[") {
			plugin.Skipf(opts.OnOutput, "gokart", "incompatible with modern Go toolchain (upstream gokart bundles tools/go/internal/pkgbits that can't read Go 1.23+ export data). Skipping.")
			return nil, nil
		}
	}

	findings, perr := parseGoKartOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// gokartResult is the SARIF-like JSON shape gokart emits.
type gokartResult struct {
	Runs []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Level     string `json:"level"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine int `json:"startLine"`
						EndLine   int `json:"endLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func parseGoKartOutput(data []byte) ([]models.Finding, error) {
	var sarif gokartResult
	if err := json.Unmarshal(plugin.ExtractJSON(data), &sarif); err != nil {
		return nil, fmt.Errorf("gokart: parse: %w", err)
	}
	var findings []models.Finding
	for _, run := range sarif.Runs {
		for _, r := range run.Results {
			file := ""
			ls, le := 0, 0
			if len(r.Locations) > 0 {
				loc := r.Locations[0].PhysicalLocation
				file = loc.ArtifactLocation.URI
				ls = loc.Region.StartLine
				le = loc.Region.EndLine
			}
			findings = append(findings, models.Finding{
				ToolName:    "gokart",
				Category:    models.CategorySAST,
				Severity:    mapGoKartSeverity(r.Level),
				Title:       r.RuleID,
				Description: r.Message.Text,
				FilePath:    file,
				LineStart:   ls,
				LineEnd:     le,
				RuleID:      r.RuleID,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapGoKartSeverity(level string) models.Severity {
	switch strings.ToLower(level) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityMedium
	}
}
