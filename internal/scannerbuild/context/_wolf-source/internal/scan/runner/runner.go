// Package runner provides parallel plugin orchestration for scan execution.
// It selects tools based on detected languages or explicit overrides, runs them
// concurrently with configurable parallelism, deduplicates findings, and
// generates stable fingerprints for each finding.
package runner

import (
	"context"
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

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scannerruntime"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// RunConfig holds all configuration for a scan run.
type RunConfig struct {
	RepoPath string
	// Target is an explicit non-repository scan target, such as a controlled
	// DAST URL or container-image archive. It is never inferred from RepoPath.
	Target     string
	ScanID     string
	UserID     string
	LeaseToken string
	Attempt    int
	Branch     string
	Registry   *plugin.Registry
	Languages  []models.Language
	Tools      []string // explicit tool list (overrides auto-detection)
	// ToolsExplicit distinguishes an intentionally empty profile/category
	// selection from legacy auto-detection, where an empty Tools slice means
	// "select by language."
	ToolsExplicit bool
	DisabledTools []string // tools to exclude from the auto-detected set
	Concurrency   int
	// HeavyConcurrency limits scanners that are expensive enough to contend
	// heavily for CPU, memory, disk, Docker, or network resources. Defaults to 1.
	HeavyConcurrency int
	// NetworkConcurrency limits scanners that need external network access.
	// Defaults to 2.
	NetworkConcurrency int
	ToolResources      map[string]ResourceSpec
	IncludePaths       []string
	ExcludePaths       []string
	Timeout            time.Duration

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
	// OnToolCancelable, when set, is called once per tool — BEFORE its
	// goroutine grabs an errgroup slot — with the per-tool cancel func.
	// The caller (API layer) stashes the cancel func in a registry
	// keyed by (scanID, toolName) so DELETE /api/scans/{id}/tools/{name}
	// can fire it. Cancelling a tool that hasn't started yet still
	// works because the goroutine checks ctx.Err() at entry and exits
	// immediately. The runner clears the registry entry via OnToolDone.
	OnToolCancelable func(toolName string, cancel context.CancelFunc)
}

type ResourceSpec struct {
	Class           string
	Timeout         time.Duration
	NetworkRequired bool
	Exclusive       bool
}

// MissingPluginSuggestion describes a plugin that could have been used but isn't installed.
type MissingPluginSuggestion struct {
	PluginName string            `json:"plugin_name"`
	Languages  []models.Language `json:"languages,omitempty"`
	Category   models.Category   `json:"category"`
}

// RunResult contains the aggregated results of a scan run.
type RunResult struct {
	Findings           []models.Finding
	ToolsRun           []string
	ToolsFailed        map[string]error
	ToolParseErrors    map[string]int
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

// Fingerprint computes the v1 stable fingerprint for legacy callers that only
// have the old helper arguments. New code should prefer identity.Apply so all
// fingerprint variants are populated.
func Fingerprint(toolName, ruleID, title, filePath string) string {
	f := models.Finding{
		ToolName: toolName,
		RuleID:   ruleID,
		Title:    title,
		FilePath: filePath,
	}
	return identity.Build(f).Stable
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

	if len(cfg.Tools) > 0 || cfg.ToolsExplicit {
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

// defaultHeavyConcurrency returns the default worker count for expensive tools.
func defaultHeavyConcurrency() int {
	return 1
}

// defaultNetworkConcurrency returns the default worker count for networked scanners.
func defaultNetworkConcurrency() int {
	return 2
}

func ResourceSpecsFromManifest(m *manifest.Manifest) map[string]ResourceSpec {
	if m == nil {
		return nil
	}
	out := make(map[string]ResourceSpec, len(m.Tools))
	for name, tool := range m.Tools {
		var timeout time.Duration
		if tool.DefaultTimeout != "" {
			timeout, _ = time.ParseDuration(tool.DefaultTimeout)
		}
		out[name] = ResourceSpec{
			Class:           tool.ResourceClass,
			Timeout:         timeout,
			NetworkRequired: tool.NetworkRequired,
			Exclusive:       tool.Exclusive,
		}
	}
	return out
}

func resourceSpecFor(p models.Plugin, resources map[string]ResourceSpec) ResourceSpec {
	if resources != nil {
		if spec, ok := resources[p.Name()]; ok {
			return spec
		}
	}
	return ResourceSpec{Class: resourceClassFromCategory(p)}
}

func resourceClassFromCategory(p models.Plugin) string {
	switch p.Category() {
	case models.CategorySCA, models.CategoryContainer, models.CategoryInfra, models.CategoryDAST:
		return "heavy"
	case models.CategorySecrets, models.CategoryQuality, models.CategoryDocs:
		return "light"
	default:
		return "medium"
	}
}

func acquireResourceSlots(ctx context.Context, spec ResourceSpec, slots resourceSlots) (func(), error) {
	var releases []func()
	acquire := func(sem chan struct{}, enabled bool) error {
		if !enabled || sem == nil {
			return nil
		}
		select {
		case sem <- struct{}{}:
			releases = append(releases, func() { <-sem })
			return nil
		case <-ctx.Done():
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
			return ctx.Err()
		}
	}

	if err := acquire(slots.exclusive, spec.Exclusive || spec.Class == "exclusive"); err != nil {
		return func() {}, err
	}
	if err := acquire(slots.network, spec.NetworkRequired || spec.Class == "network"); err != nil {
		return func() {}, err
	}
	if err := acquire(slots.heavy, spec.Class == "heavy"); err != nil {
		return func() {}, err
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}

func timeoutForTool(defaultTimeout time.Duration, spec ResourceSpec) time.Duration {
	if spec.Timeout > 0 {
		return spec.Timeout
	}
	return defaultTimeout
}

type resourceSlots struct {
	heavy     chan struct{}
	network   chan struct{}
	exclusive chan struct{}
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
	heavyConcurrency := cfg.HeavyConcurrency
	if heavyConcurrency <= 0 {
		heavyConcurrency = defaultHeavyConcurrency()
	}
	networkConcurrency := cfg.NetworkConcurrency
	if networkConcurrency <= 0 {
		networkConcurrency = defaultNetworkConcurrency()
	}
	slots := resourceSlots{
		heavy:     make(chan struct{}, heavyConcurrency),
		network:   make(chan struct{}, networkConcurrency),
		exclusive: make(chan struct{}, 1),
	}

	result := &RunResult{
		ToolsFailed:     make(map[string]error),
		ToolParseErrors: make(map[string]int),
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
		// Per-tool context, derived from the errgroup's gctx. Cancelling
		// the per-tool ctx kills just this tool (queued or running);
		// cancelling gctx (i.e. cancelling the scan) cascades to all
		// per-tool ctxs. Register the cancel func via OnToolCancelable
		// BEFORE Go-ing the closure so DELETE /api/scans/{id}/tools/{name}
		// can land between SelectTools and goroutine start.
		toolCtx, toolCancel := context.WithCancel(gctx)
		if cfg.OnToolCancelable != nil {
			cfg.OnToolCancelable(p.Name(), toolCancel)
		}
		g.Go(func() error {
			defer toolCancel() // release in case the tool completes normally
			toolName := p.Name()
			parseErrors := 0
			toolStart := time.Now()

			// If this tool was cancelled BEFORE it acquired a slot
			// (e.g. waited behind a long-running peer, then user
			// clicked X on it in the UI), exit cleanly without running.
			if toolCtx.Err() != nil {
				log.Info().Str("tool", toolName).Msg("tool cancelled before start; skipping")
				if cfg.OnToolDone != nil {
					cfg.OnToolDone(toolName, nil, toolCtx.Err())
				}
				mu.Lock()
				result.ToolsRun = append(result.ToolsRun, toolName)
				result.ToolsFailed[toolName] = toolCtx.Err()
				mu.Unlock()
				return nil // never fail the errgroup — sibling tools keep going
			}

			spec := resourceSpecFor(p, cfg.ToolResources)
			releaseResources, acquireErr := acquireResourceSlots(toolCtx, spec, slots)
			if acquireErr != nil {
				log.Info().Str("tool", toolName).Err(acquireErr).Msg("tool cancelled before acquiring scanner resource slot; skipping")
				if cfg.OnToolDone != nil {
					cfg.OnToolDone(toolName, nil, acquireErr)
				}
				mu.Lock()
				result.ToolsRun = append(result.ToolsRun, toolName)
				result.ToolsFailed[toolName] = acquireErr
				mu.Unlock()
				return nil
			}
			defer releaseResources()

			log.Info().Str("tool", toolName).Str("resource_class", spec.Class).Msg("tool starting")

			if cfg.OnToolStart != nil {
				cfg.OnToolStart(toolName)
			}

			runtimeNetworkClass := "offline"
			if spec.NetworkRequired || spec.Class == "network" {
				runtimeNetworkClass = "network-required"
			}
			executionCtx := scannerruntime.WithExecutionIdentity(toolCtx, scannerruntime.Identity{
				ScanID: cfg.ScanID, ToolName: toolName, UserID: cfg.UserID,
				LeaseToken: cfg.LeaseToken, Attempt: cfg.Attempt,
				NetworkClass: runtimeNetworkClass,
			})
			toolOpts := models.ExecuteOpts{
				RepoPath:     cfg.RepoPath,
				Target:       cfg.Target,
				Branch:       cfg.Branch,
				IncludePaths: cfg.IncludePaths,
				ExcludePaths: cfg.ExcludePaths,
				Timeout:      timeoutForTool(timeout, spec),
				ContainerCfg: cfg.ContainerCfg,
			}
			toolOpts.OnParseError = func(error) { parseErrors++ }
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

			findings, err := p.Execute(executionCtx, toolOpts)

			elapsed := time.Since(toolStart)

			// Assign fingerprints + apply the deterministic knowledge base
			// (fine_category, fix_strategy_id) so the dedupe step can merge
			// cross-tool matches by canonical category.
			for i := range findings {
				f := &findings[i]
				applyKnowledge(f)
				identity.Apply(f)
			}

			mu.Lock()
			result.ToolsRun = append(result.ToolsRun, toolName)
			if err != nil {
				result.ToolsFailed[toolName] = err
			}
			result.ToolParseErrors[toolName] = parseErrors
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
