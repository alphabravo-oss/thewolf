package security

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// BearerPlugin runs Bearer to detect privacy / PII / data-flow risks
// (GDPR, HIPAA, PCI categories). Different shape from CVE-focused scanners
// — it tracks "this code processes personally-identifiable data of type X
// in a way that violates pattern Y".
//
// Bearer outputs JSON keyed by severity (critical/high/medium/low/warning).
// Each entry has rule ID, description, filename, line range, and the
// data type flagged.
type BearerPlugin struct{}

func init() { plugin.Register(&BearerPlugin{}) }

func (p *BearerPlugin) Name() string                 { return "bearer" }
func (p *BearerPlugin) Category() models.Category    { return models.CategorySAST }
func (p *BearerPlugin) Languages() []models.Language { return nil }

func (p *BearerPlugin) CheckAvailable() bool { return container.IsScannersReady() }

func (p *BearerPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	// bearer publishes amd64-only images. On arm64 hosts (Apple Silicon,
	// ARM Linux), Docker runs them via qemu emulation, but bearer's Go
	// binary crashes with "lfstack.push invalid packing" — an upstream
	// Go runtime bug under x86_64 emulation. Skip cleanly with guidance
	// rather than surface a 200-line Go traceback.
	if plugin.IsArm64Host() {
		plugin.Skipf(opts.OnOutput, "bearer", "no native arm64 image; amd64 binary crashes under qemu emulation (upstream Go runtime bug). Run on amd64 host or omit bearer from --tools.")
		return nil, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	// bearer holds the whole repo AST in memory during data-flow tracking,
	// which OOMs at the 2g default on non-trivial repos (visible as a Go
	// runtime trace from the bearer binary's gc workers). 4g leaves room.
	// Rule packs are fetched from GitHub Releases on first use of each
	// language and stored under ~/.bearer. HOME=/tmp would throw that
	// cache away with the container, so every scan re-downloaded
	// javascript.tar.gz (and timed out on a slow GitHub). Persist on
	// the shared wolf-db volume the same way trivy/grype persist DBs.
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	home := "/tmp"
	if cfg.DBVolume != "" {
		home = "/var/lib/wolf-db/bearer"
	}
	cmd := container.CommandContext(ctx, cfg,
		container.Options{
			RepoDir:        opts.RepoPath,
			ExtraEnv:       map[string]string{"HOME": home},
			MemoryOverride: "4g",
		},
		"bearer", "scan", "/scan", "--format", "json", "--quiet", "--exit-code", "0")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, plugin.WrapExecError("bearer", err)
	}

	findings, perr := parseBearerOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// bearerOutput is keyed by severity (e.g. "high", "medium").
type bearerHit struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	CWE          []string `json:"cwe_ids"`
	Filename     string   `json:"filename"`
	FullFilename string   `json:"full_filename"`
	LineNumber   int      `json:"line_number"`
	SnippetEnd   int      `json:"snippet_end_line"`
	DataType     struct {
		Name string `json:"name"`
	} `json:"data_type"`
}

func parseBearerOutput(data []byte) ([]models.Finding, error) {
	js := plugin.ExtractJSON(data)
	// Parse as map[severity]→[]bearerHit. Tolerate unknown severity buckets.
	var bySev map[string][]bearerHit
	if err := json.Unmarshal(js, &bySev); err != nil {
		return nil, fmt.Errorf("bearer: parse: %w", err)
	}
	var findings []models.Finding
	for sev, hits := range bySev {
		s := mapBearerSeverity(sev)
		for _, h := range hits {
			file := h.Filename
			if file == "" {
				file = h.FullFilename
			}
			cwe := ""
			if len(h.CWE) > 0 {
				cwe = h.CWE[0]
				if !strings.HasPrefix(cwe, "CWE-") {
					cwe = "CWE-" + cwe
				}
			}
			desc := h.Description
			if h.DataType.Name != "" {
				desc = fmt.Sprintf("Data type: %s. %s", h.DataType.Name, desc)
			}
			findings = append(findings, models.Finding{
				ToolName:    "bearer",
				Category:    models.CategorySAST,
				Severity:    s,
				Title:       h.Title,
				Description: desc,
				FilePath:    file,
				LineStart:   h.LineNumber,
				LineEnd:     h.SnippetEnd,
				RuleID:      h.ID,
				CWEID:       cwe,
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapBearerSeverity(s string) models.Severity {
	switch strings.ToLower(s) {
	case "critical":
		return models.SeverityCritical
	case "high":
		return models.SeverityHigh
	case "medium":
		return models.SeverityMedium
	case "low":
		return models.SeverityLow
	case "warning", "info":
		return models.SeverityInfo
	default:
		return models.SeverityInfo
	}
}
