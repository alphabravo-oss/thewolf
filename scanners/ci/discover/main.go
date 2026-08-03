// Command discover performs the release factory's read-only scanner freshness
// pass. It emits a stable, machine-readable report; it never changes pins,
// opens proposals, publishes images, or mutates runtime release state.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

const reportSchema = "wolf.scanners.discovery/v1"

type options struct {
	manifestPath    string
	outputPath      string
	summaryPath     string
	definitionSHA   string
	concurrency     int
	perToolTimeout  time.Duration
	overallTimeout  time.Duration
	minimumCoverage float64
	failOnError     bool
}

type counts struct {
	Total           int `json:"total"`
	Current         int `json:"current"`
	UpdateAvailable int `json:"update_available"`
	Unknown         int `json:"unknown"`
	Failed          int `json:"failed"`
	Checked         int `json:"checked"`
}

type report struct {
	SchemaVersion    string                       `json:"schema_version"`
	DefinitionCommit string                       `json:"definition_commit,omitempty"`
	ManifestSHA256   string                       `json:"manifest_sha256"`
	GeneratedAt      time.Time                    `json:"generated_at"`
	Coverage         float64                      `json:"coverage"`
	Counts           counts                       `json:"counts"`
	Results          []models.ScannerVersionCheck `json:"results"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "scanner discovery:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, now func() time.Time) error {
	var opts options
	fs := flag.NewFlagSet("scanner-discovery", flag.ContinueOnError)
	fs.StringVar(&opts.manifestPath, "manifest", "scanners/tools.yaml", "scanner manifest path")
	fs.StringVar(&opts.outputPath, "output", "", "JSON report output path (stdout when empty)")
	fs.StringVar(&opts.summaryPath, "github-summary", "", "optional GitHub step-summary path")
	fs.StringVar(&opts.definitionSHA, "definition-commit", "", "full definition Git commit")
	fs.IntVar(&opts.concurrency, "concurrency", 6, "maximum concurrent update-source checks")
	fs.DurationVar(&opts.perToolTimeout, "per-tool-timeout", 30*time.Second, "timeout per update source")
	fs.DurationVar(&opts.overallTimeout, "overall-timeout", 15*time.Minute, "timeout for the discovery pass")
	fs.Float64Var(&opts.minimumCoverage, "minimum-coverage", 0, "required successful coverage from 0 through 1")
	fs.BoolVar(&opts.failOnError, "fail-on-check-error", false, "fail when any source check fails")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateOptions(opts); err != nil {
		return err
	}

	data, err := os.ReadFile(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	m, err := manifest.LoadBytes(data, opts.manifestPath)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.overallTimeout)
	defer cancel()

	checker := latest.Checker{Now: now}
	results := checkAll(runCtx, checker, m, opts.concurrency, opts.perToolTimeout)
	rep := makeReport(results, data, opts.definitionSHA, now().UTC())

	encoded, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	encoded = append(encoded, '\n')
	if opts.outputPath == "" {
		if _, err := os.Stdout.Write(encoded); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	} else if err := writeFile(opts.outputPath, encoded); err != nil {
		return err
	}
	if opts.summaryPath != "" {
		if err := writeSummary(opts.summaryPath, rep); err != nil {
			return err
		}
	}

	if err := runCtx.Err(); err != nil {
		return fmt.Errorf("overall timeout: %w", err)
	}
	if rep.Coverage < opts.minimumCoverage {
		return fmt.Errorf("coverage %.1f%% is below required %.1f%%", rep.Coverage*100, opts.minimumCoverage*100)
	}
	if opts.failOnError && rep.Counts.Failed > 0 {
		return fmt.Errorf("%d update-source checks failed", rep.Counts.Failed)
	}
	return nil
}

func validateOptions(opts options) error {
	if opts.concurrency < 1 || opts.concurrency > 32 {
		return errors.New("concurrency must be from 1 through 32")
	}
	if opts.perToolTimeout < time.Second || opts.perToolTimeout > 5*time.Minute {
		return errors.New("per-tool-timeout must be from 1s through 5m")
	}
	if opts.overallTimeout < opts.perToolTimeout || opts.overallTimeout > time.Hour {
		return errors.New("overall-timeout must be at least per-tool-timeout and no more than 1h")
	}
	if opts.minimumCoverage < 0 || opts.minimumCoverage > 1 {
		return errors.New("minimum-coverage must be from 0 through 1")
	}
	if opts.definitionSHA != "" && !isFullSHA(opts.definitionSHA) {
		return errors.New("definition-commit must be a 40-character lowercase Git SHA")
	}
	return nil
}

func isFullSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func checkAll(
	ctx context.Context,
	checker latest.Checker,
	m *manifest.Manifest,
	concurrency int,
	perToolTimeout time.Duration,
) []models.ScannerVersionCheck {
	names := m.Names()
	jobs := make(chan string)
	results := make(chan models.ScannerVersionCheck, len(names))
	var workers sync.WaitGroup

	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for name := range jobs {
				if ctx.Err() != nil {
					results <- failedResult(name, m.Tools[name], "discovery cancelled")
					continue
				}
				checkCtx, cancel := context.WithTimeout(ctx, perToolTimeout)
				result := checker.Check(checkCtx, name, m.Tools[name])
				cancel()
				result.Error = sanitizeError(result.Error)
				results <- result
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, name := range names {
			jobs <- name
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	out := make([]models.ScannerVersionCheck, 0, len(names))
	for result := range results {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ToolName < out[j].ToolName })
	return out
}

func failedResult(name string, tool manifest.Tool, message string) models.ScannerVersionCheck {
	return models.ScannerVersionCheck{
		ToolName:      name,
		PinnedVersion: tool.PinnedVersion,
		Status:        models.ScannerVersionCheckFailed,
		SourceType:    tool.UpdateSource.Type,
		Error:         message,
		CheckedAt:     time.Now().UTC(),
	}
}

func sanitizeError(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	const max = 500
	if len(value) > max {
		value = value[:max] + "…"
	}
	return value
}

func makeReport(
	results []models.ScannerVersionCheck,
	manifestData []byte,
	definitionSHA string,
	generatedAt time.Time,
) report {
	rep := report{
		SchemaVersion:    reportSchema,
		DefinitionCommit: definitionSHA,
		ManifestSHA256:   digest(manifestData),
		GeneratedAt:      generatedAt,
		Results:          results,
		Counts:           counts{Total: len(results)},
	}
	for _, result := range results {
		switch result.Status {
		case models.ScannerVersionCurrent:
			rep.Counts.Current++
			rep.Counts.Checked++
		case models.ScannerVersionUpdateAvailable:
			rep.Counts.UpdateAvailable++
			rep.Counts.Checked++
		case models.ScannerVersionCheckFailed:
			rep.Counts.Failed++
		default:
			rep.Counts.Unknown++
		}
	}
	if rep.Counts.Total > 0 {
		rep.Coverage = float64(rep.Counts.Checked) / float64(rep.Counts.Total)
	}
	return rep
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func writeSummary(path string, rep report) error {
	var b strings.Builder
	fmt.Fprintln(&b, "## Scanner update discovery")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- Definition: `%s`\n", rep.DefinitionCommit)
	fmt.Fprintf(&b, "- Manifest: `%s`\n", rep.ManifestSHA256)
	fmt.Fprintf(&b, "- Coverage: **%.1f%%** (%d of %d)\n", rep.Coverage*100, rep.Counts.Checked, rep.Counts.Total)
	fmt.Fprintf(&b, "- Updates available: **%d**\n", rep.Counts.UpdateAvailable)
	fmt.Fprintf(&b, "- Source failures: **%d**\n", rep.Counts.Failed)
	fmt.Fprintf(&b, "- Unknown/manual sources: **%d**\n", rep.Counts.Unknown)

	var updates []models.ScannerVersionCheck
	for _, result := range rep.Results {
		if result.Status == models.ScannerVersionUpdateAvailable {
			updates = append(updates, result)
		}
	}
	if len(updates) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "| Tool | Pinned | Available | Source |")
		fmt.Fprintln(&b, "|---|---:|---:|---|")
		for _, result := range updates {
			fmt.Fprintf(
				&b,
				"| %s | `%s` | `%s` | %s |\n",
				markdownCell(result.ToolName),
				markdownCell(result.PinnedVersion),
				markdownCell(result.LatestVersion),
				markdownCell(result.SourceType),
			)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write GitHub summary: %w", err)
	}
	return nil
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", `\|`)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return value
}
