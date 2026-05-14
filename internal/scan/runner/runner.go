// Package runner provides parallel plugin orchestration for scan execution.
// It selects tools based on detected languages or explicit overrides, runs them
// concurrently with configurable parallelism, deduplicates findings, and
// generates stable fingerprints for each finding.
package runner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// RunConfig holds all configuration for a scan run.
type RunConfig struct {
	RepoPath      string
	Branch        string
	Registry      *plugin.Registry
	Languages     []models.Language
	Tools         []string // explicit tool list (overrides auto-detection)
	DisabledTools []string // tools to exclude from the auto-detected set
	Concurrency   int
	IncludePaths  []string
	ExcludePaths  []string
	Timeout       time.Duration

	// ContainerCfg is the container-backend runtime config. Wolf-slim
	// startup builds a *container.Config and threads it through here so
	// every plugin in this scan run uses the same image, network policy,
	// resource caps, and path translation. Typed as `any` to avoid an
	// import cycle (see models.ExecuteOpts.ContainerCfg).
	ContainerCfg any

	// RawOutputDir, when non-empty, causes the runner to persist each
	// plugin's raw pre-parse output (as forwarded via models.ExecuteOpts.
	// OnRawOutput) to <RawOutputDir>/<tool>.<ext>. The directory is
	// created on demand. Plugins that don't call OnRawOutput simply don't
	// produce a file.
	RawOutputDir string

	OnToolsSelected func(toolNames []string) // called with the full list before any tool starts
	OnToolStart     func(toolName string)
	OnToolDone      func(toolName string, findings []models.Finding, err error)
	OnToolOutput    func(toolName string, line string)
	OnToolRaw       func(toolName string, data []byte, ext string) // optional: observe raw bytes
}

// MissingPluginSuggestion describes a plugin that could have been used but isn't installed.
type MissingPluginSuggestion struct {
	PluginName string           `json:"plugin_name"`
	Languages  []models.Language `json:"languages,omitempty"`
	Category   models.Category  `json:"category"`
}

// RunResult contains the aggregated results of a scan run.
type RunResult struct {
	Findings           []models.Finding
	ToolsRun           []string
	ToolsFailed        map[string]error
	ToolsSkipped       []string
	MissingSuggestions []MissingPluginSuggestion
	Duration           time.Duration
}

// severityRank returns a numeric rank for severity comparison.
// Higher rank means more severe.
func severityRank(s models.Severity) int {
	switch s {
	case models.SeverityCritical:
		return 5
	case models.SeverityHigh:
		return 4
	case models.SeverityMedium:
		return 3
	case models.SeverityLow:
		return 2
	case models.SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Fingerprint computes a stable SHA256 fingerprint for a finding.
// It uses tool_name + ":" + identifier + ":" + file_path, where identifier
// is rule_id if non-empty, otherwise title.
func Fingerprint(toolName, ruleID, title, filePath string) string {
	identifier := ruleID
	if identifier == "" {
		identifier = title
	}
	input := toolName + ":" + identifier + ":" + filePath
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

// dedupKey returns the deduplication key for a finding. When a fine_category
// is set (from the knowledge-base lookup that happens at parse time), the
// key uses it so two tools reporting the same SQL injection at the same
// line collapse into one record. When no fine_category is known, falls back
// to rule_id / title — preserving the pre-Phase-2 behavior for uncategorized
// findings.
func dedupKey(f models.Finding) string {
	if f.FineCategory != "" {
		return fmt.Sprintf("%s:%d:%s", f.FilePath, f.LineStart, f.FineCategory)
	}
	identifier := f.RuleID
	if identifier == "" {
		identifier = f.Title
	}
	return fmt.Sprintf("%s:%d:%s", f.FilePath, f.LineStart, identifier)
}

// confidenceFromCorroboration converts the number of distinct tools that
// flagged a finding into a deterministic confidence label. The thresholds
// are deliberately blunt — finer gradations require ground truth we don't
// have.
func confidenceFromCorroboration(n int) string {
	switch {
	case n >= 3:
		return "high"
	case n == 2:
		return "medium"
	default:
		return "low"
	}
}

// uniqStrings preserves first-seen order while removing duplicates.
func uniqStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Deduplicate removes duplicate findings, merging cross-tool matches into a
// single record annotated with CorroboratedBy + Confidence. Two findings are
// duplicates when they share (file, start_line, fine_category) — or (file,
// start_line, rule_id) when no fine_category is known.
//
// When duplicates exist the highest-severity record is kept as the primary,
// but every contributing tool's name is preserved on CorroboratedBy so the
// renderer can show "flagged by gosec + semgrep".
func Deduplicate(findings []models.Finding) []models.Finding {
	type bucket struct {
		idx   int      // position in result
		tools []string // every tool that contributed a match
	}
	seen := make(map[string]*bucket, len(findings))
	result := make([]models.Finding, 0, len(findings))

	for _, f := range findings {
		key := dedupKey(f)
		if b, exists := seen[key]; exists {
			b.tools = append(b.tools, f.ToolName)
			// Promote the higher-severity record to primary while
			// preserving the running tool list.
			if severityRank(f.Severity) > severityRank(result[b.idx].Severity) {
				existingTools := b.tools
				result[b.idx] = f
				b.tools = existingTools
			}
		} else {
			seen[key] = &bucket{idx: len(result), tools: []string{f.ToolName}}
			result = append(result, f)
		}
	}

	// Annotate primaries with corroboration metadata.
	for _, b := range seen {
		tools := uniqStrings(b.tools)
		result[b.idx].CorroboratedBy = tools
		result[b.idx].Confidence = confidenceFromCorroboration(len(tools))
	}

	return result
}

// applyKnowledge enriches a finding with FineCategory + FixStrategyID from
// the knowledge base. Safe to call on already-enriched findings (it preserves
// existing values). Returns true when any field was set.
func applyKnowledge(f *models.Finding) bool {
	if f.FineCategory != "" {
		return false
	}
	fc, fs := knowledge.Categorize(f.ToolName, f.RuleID)
	if fc == "" {
		return false
	}
	f.FineCategory = fc
	f.FixStrategyID = fs
	return true
}

// SelectTools determines which plugins to run based on the RunConfig.
// Priority:
//  1. If Tools is non-empty, use only those (explicit override).
//  2. Otherwise ("auto" mode), select plugins matching detected Languages.
//
// In all cases, DisabledTools are removed from the final set.
func SelectTools(cfg RunConfig) []models.Plugin {
	if cfg.Registry == nil {
		return nil
	}

	var selected []models.Plugin

	if len(cfg.Tools) > 0 {
		// Explicit tool list overrides everything.
		for _, name := range cfg.Tools {
			if p, err := cfg.Registry.Get(name); err == nil {
				selected = append(selected, p)
			}
		}
	} else {
		// Auto mode: collect plugins matching detected languages.
		seen := make(map[string]bool)
		for _, lang := range cfg.Languages {
			for _, p := range cfg.Registry.GetByLanguage(lang) {
				if !seen[p.Name()] {
					seen[p.Name()] = true
					selected = append(selected, p)
				}
			}
		}
		// If no languages specified, fall back to all plugins.
		if len(cfg.Languages) == 0 {
			selected = cfg.Registry.GetAll()
		}
	}

	// Apply disabled tools list.
	if len(cfg.DisabledTools) > 0 {
		disabledSet := make(map[string]bool, len(cfg.DisabledTools))
		for _, name := range cfg.DisabledTools {
			disabledSet[name] = true
		}
		filtered := make([]models.Plugin, 0, len(selected))
		for _, p := range selected {
			if !disabledSet[p.Name()] {
				filtered = append(filtered, p)
			}
		}
		selected = filtered
	}

	return selected
}

// defaultConcurrency returns the default worker count: 4.
// Kept conservative because some tools (e.g. semgrep) are CPU-intensive.
func defaultConcurrency() int {
	return 4
}

// sniffExt looks at the first non-whitespace bytes of data and returns a best-
// guess canonical extension ("json", "sarif", "xml", "txt"). SARIF is reported
// as "sarif" only if a top-level "$schema" mentions sarif; otherwise generic
// JSON falls back to "json".
func sniffExt(data []byte) string {
	trim := strings.TrimLeft(string(data), " \t\r\n")
	switch {
	case strings.HasPrefix(trim, "<"):
		return "xml"
	case strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "["):
		if strings.Contains(trim[:min(len(trim), 512)], "sarif") {
			return "sarif"
		}
		return "json"
	default:
		return "txt"
	}
}

// extractStderr extracts stderr content from an exec.ExitError if present.
// Many tools write diagnostic messages to stderr before exiting non-zero;
// this function recovers that information from the Go error chain.
func extractStderr(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}

// Run is the main entry point for scan execution. It selects tools, checks
// availability, runs plugins in parallel, deduplicates findings, and assigns
// fingerprints.
func Run(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	log := wolflog.Component("runner")
	start := time.Now()

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency()
	}

	result := &RunResult{
		ToolsFailed: make(map[string]error),
	}

	// Select tools.
	plugins := SelectTools(cfg)

	// Check availability and partition into runnable vs skipped.
	var runnable []models.Plugin
	for _, p := range plugins {
		if !p.CheckAvailable() {
			log.Warn().Str("tool", p.Name()).Msg("tool not available, skipping")
			result.ToolsSkipped = append(result.ToolsSkipped, p.Name())
			result.MissingSuggestions = append(result.MissingSuggestions, MissingPluginSuggestion{
				PluginName: p.Name(),
				Languages:  p.Languages(),
				Category:   p.Category(),
			})
		} else {
			runnable = append(runnable, p)
		}
	}

	if len(runnable) == 0 {
		log.Warn().Msg("no runnable tools found")
		result.Duration = time.Since(start)
		return result, nil
	}

	// Notify caller of the full tool list before starting execution.
	if cfg.OnToolsSelected != nil {
		names := make([]string, len(runnable))
		for i, p := range runnable {
			names[i] = p.Name()
		}
		sort.Strings(names)
		log.Info().Strs("tools", names).Int("count", len(names)).Msg("tools selected for scan")
		cfg.OnToolsSelected(names)
	}

	// Prepare execution options.
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	// Run plugins in parallel using errgroup with limited concurrency.
	var mu sync.Mutex
	var allFindings []models.Finding

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for _, p := range runnable {
		p := p // capture loop variable
		g.Go(func() error {
			toolName := p.Name()
			toolStart := time.Now()

			log.Info().Str("tool", toolName).Msg("tool starting")

			if cfg.OnToolStart != nil {
				cfg.OnToolStart(toolName)
			}

			toolOpts := models.ExecuteOpts{
				RepoPath:     cfg.RepoPath,
				Branch:       cfg.Branch,
				IncludePaths: cfg.IncludePaths,
				ExcludePaths: cfg.ExcludePaths,
				Timeout:      timeout,
				ContainerCfg: cfg.ContainerCfg,
			}
			if cfg.OnToolOutput != nil {
				toolOpts.OnOutput = func(line string) {
					cfg.OnToolOutput(toolName, line)
				}
			}
			// Capture raw pre-parse tool output so we can persist it to
			// disk and/or hand it to observers. Plugins call this via
			// plugin.SaveRaw(opts, data, ext) right after they get bytes
			// back from the tool but before parsing.
			toolOpts.OnRawOutput = func(data []byte, ext string) {
				if cfg.RawOutputDir != "" && len(data) > 0 {
					if mkErr := os.MkdirAll(cfg.RawOutputDir, 0o750); mkErr == nil {
						if ext == "" {
							ext = sniffExt(data)
						}
						path := filepath.Join(cfg.RawOutputDir, toolName+"."+ext)
						if werr := os.WriteFile(path, data, 0o600); werr != nil {
							log.Warn().Str("tool", toolName).Err(werr).Msg("failed to write raw tool output")
						}
					}
				}
				if cfg.OnToolRaw != nil {
					cfg.OnToolRaw(toolName, data, ext)
				}
			}

			findings, err := p.Execute(gctx, toolOpts)

			elapsed := time.Since(toolStart)

			// Assign fingerprints + apply the deterministic knowledge base
			// (fine_category, fix_strategy_id) so the dedupe step can merge
			// cross-tool matches by canonical category.
			for i := range findings {
				f := &findings[i]
				if f.Fingerprint == "" {
					f.Fingerprint = Fingerprint(f.ToolName, f.RuleID, f.Title, f.FilePath)
				}
				applyKnowledge(f)
			}

			mu.Lock()
			result.ToolsRun = append(result.ToolsRun, toolName)
			if err != nil {
				result.ToolsFailed[toolName] = err
			}
			if len(findings) > 0 {
				allFindings = append(allFindings, findings...)
			}
			mu.Unlock()

			if err != nil {
				log.Error().Str("tool", toolName).Dur("elapsed", elapsed).Err(err).Msg("tool failed")

				// Emit error details through OnToolOutput so they appear in
				// log files and SSE streams — otherwise failed tools have
				// zero visible output and failures are undiagnosable.
				if cfg.OnToolOutput != nil {
					cfg.OnToolOutput(toolName, fmt.Sprintf("[ERROR] %s failed: %v", toolName, err))
					if stderr := extractStderr(err); stderr != "" {
						for _, line := range strings.Split(stderr, "\n") {
							cfg.OnToolOutput(toolName, "[STDERR] "+line)
						}
					}
					// Try to diagnose the failure and provide actionable guidance.
					if diag := plugin.DiagnoseExecError(toolName, err, extractStderr(err)); diag != nil {
						plugin.EmitDiagnostic(func(line string) {
							cfg.OnToolOutput(toolName, line)
						}, diag)
					}
				}
			} else {
				log.Info().Str("tool", toolName).Dur("elapsed", elapsed).Int("findings", len(findings)).Msg("tool completed")
			}

			if cfg.OnToolDone != nil {
				cfg.OnToolDone(toolName, findings, err)
			}

			// We don't return errors here because a single tool failure
			// should not cancel the entire scan.
			return nil
		})
	}

	// errgroup.Wait never returns an error here since we always return nil above,
	// but we call it to ensure all goroutines complete.
	_ = g.Wait()

	// Deduplicate findings (fingerprints already assigned per-tool above).
	result.Findings = Deduplicate(allFindings)

	// Sort ToolsRun for deterministic output.
	sort.Strings(result.ToolsRun)

	result.Duration = time.Since(start)

	log.Info().
		Dur("elapsed", result.Duration).
		Int("findings", len(result.Findings)).
		Int("tools_run", len(result.ToolsRun)).
		Int("tools_failed", len(result.ToolsFailed)).
		Int("tools_skipped", len(result.ToolsSkipped)).
		Msg("scan run complete")

	return result, nil
}
