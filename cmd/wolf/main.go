// Package main is the wolf CLI entrypoint.
//
// Subcommands:
//
//	wolf serve            — start the HTTP API + UI server
//	wolf scan             — run a one-shot scan against a repo path
//	wolf doctor           — diagnose the container scanner backend
//	wolf pull scanners    — pre-pull every configured scanner image
//	wolf version          — print version info
//
// Configuration is read from environment variables (12-factor); wolf.yaml
// is honored only via env-overlay scripts in docker-compose. See PLAN.md
// §5.6 for the full env-var reference.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/cli"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/enrich"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/loop/controller"
	"github.com/alphabravocompany/thewolf/internal/loop/tracker"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/scan/planner"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	scannermanifest "github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
	_ "github.com/alphabravocompany/thewolf/plugins"
)

// Version metadata. Overridden at link time via:
//
//	-ldflags "-X main.version=X.Y.Z -X main.commit=abc -X main.buildDate=..."
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "wolf",
		Short:         "The Wolf — multi-tool code analysis engine",
		SilenceUsage:  true, // don't dump --help on every RunE error
		SilenceErrors: false,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initLogger()
		},
	}

	// Global flags for the API-client commands (--server/--token/--context/-o).
	cli.AddGlobalFlags(rootCmd)

	// The local one-shot `scan` and AI auto-remediation `loop` commands
	// gain the API-client subcommands so `wolf scan list`, `wolf loop list`,
	// etc. drive the server. Bare `wolf scan` / `wolf loop` keep their
	// local one-shot behavior.
	scanCmd := newScanCmd()
	cli.AddScanSubcommands(scanCmd)
	loopCmd := newLoopCmd()
	cli.AddLoopSubcommands(loopCmd)

	rootCmd.AddCommand(
		newServeCmd(),
		newDoctorCmd(),
		newPullCmd(),
		newVersionCmd(),
		scanCmd,
		newEnrichCmd(),
		loopCmd,
	)
	// Every API endpoint as a `wolf <resource> <verb>` command (loop's API
	// subcommands attach to loopCmd above instead of registering separately).
	rootCmd.AddCommand(cli.NewCommandGroups()...)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(cli.ExitCodeFor(err))
	}
}

// initLogger configures the global wolflog using WOLF_LOG_LEVEL/WOLF_LOG_JSON env.
func initLogger() {
	level := zerolog.InfoLevel
	if v := os.Getenv("WOLF_LOG_LEVEL"); v != "" {
		if parsed, err := zerolog.ParseLevel(strings.ToLower(v)); err == nil {
			level = parsed
		}
	}
	json := strings.EqualFold(os.Getenv("WOLF_LOG_JSON"), "true") ||
		strings.EqualFold(os.Getenv("WOLF_LOG_JSON"), "1")
	wolflog.Init(os.Stderr, level, json)
}

// openStore opens the configured database backend. Defaults to SQLite at
// ~/.wolf/wolf.db.
func openStore() (db.Store, error) {
	driver := envOr("WOLF_DB_DRIVER", "sqlite")
	switch strings.ToLower(driver) {
	case "sqlite":
		dsn := os.Getenv("WOLF_DB_DSN")
		if dsn == "" {
			home, _ := os.UserHomeDir()
			_ = os.MkdirAll(home+"/.wolf", 0o750)
			dsn = home + "/.wolf/wolf.db"
		}
		return db.NewSQLite(dsn)
	case "postgres":
		dsn := os.Getenv("WOLF_DB_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("WOLF_DB_DSN required for postgres driver")
		}
		return db.NewPostgres(dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
	}
}

// installScannerBackend wires the container backend into the process. Failures
// here are warnings, not fatal — operators may want to run `wolf doctor` and
// `wolf pull scanners` to fix the situation interactively. Subcommands that
// can run without the scanner backend (e.g. `wolf version`) skip this.
func installScannerBackend(ctx context.Context) error {
	_, err := scanners.LoadAndInstall(ctx)
	return err
}

// --- serve ------------------------------------------------------------------

func newServeCmd() *cobra.Command {
	var (
		addr         string
		skipScanInit bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the wolf HTTP API + UI server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if !skipScanInit {
				if err := installScannerBackend(ctx); err != nil {
					wolflog.L().Warn().Err(err).Msg("scanner backend not ready at startup; UI will surface diagnostics")
				}
			}

			store, err := openStore()
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer store.Close()

			routes.AppVersion = version
			routes.BuildCommit = commit
			routes.BuildDate = buildDate

			// JWT secret resolution order:
			//   1. WOLF_MASTER_KEY env — preferred for production / clustered
			//      deployments (every replica must share the same secret).
			//   2. ~/.wolf/jwt-secret — auto-persisted dev fallback so tokens
			//      survive `wolf serve` restarts without forcing the operator
			//      to remember an env var. Generated lazily on first run,
			//      written 0600.
			//   3. Random per-process — only when (2) is unwritable (e.g.
			//      no home dir). Logs a warning because every restart now
			//      kicks every logged-in user out.
			//
			// crypto/rand reads exactly the requested number of bytes from the
			// OS CSPRNG. A prior bug used os.ReadFile("/dev/urandom") which
			// streamed the (infinite) device until OOM — keep that lesson by
			// staying on crypto/rand.
			secret := []byte(envOr("WOLF_MASTER_KEY", ""))
			if len(secret) == 0 {
				if home, herr := os.UserHomeDir(); herr == nil && home != "" {
					path := filepath.Join(home, ".wolf", "jwt-secret")
					if data, rerr := os.ReadFile(path); rerr == nil && len(data) >= 32 { // #nosec G304 -- path is validated upstream (scan-root / artifact-dir / configured input)
						secret = data
					} else {
						secret = make([]byte, 32)
						if _, err := cryptorand.Read(secret); err != nil {
							return fmt.Errorf("read CSPRNG for JWT secret: %w", err)
						}
						if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr == nil {
							if wErr := os.WriteFile(path, secret, 0o600); wErr == nil {
								wolflog.L().Info().Str("path", path).Msg("JWT secret persisted; tokens will survive restart")
							} else {
								wolflog.L().Warn().Err(wErr).Msg("WOLF_MASTER_KEY unset and ~/.wolf/jwt-secret not writable; tokens won't survive restart")
							}
						}
					}
				} else {
					secret = make([]byte, 32)
					if _, err := cryptorand.Read(secret); err != nil {
						return fmt.Errorf("read CSPRNG for JWT secret: %w", err)
					}
					wolflog.L().Warn().Msg("WOLF_MASTER_KEY unset and no home dir; tokens won't survive restart")
				}
			}
			auth.SetJWTSecret(secret)

			srv := api.NewServer(store, addr)
			wolflog.L().Info().Str("addr", addr).Msg("wolf serve")

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Start() }()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				shutdownCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
				defer sc()
				return srv.Shutdown(shutdownCtx)
			}
		},
	}
	cmd.Flags().StringVar(&addr, "bind", ":8778", "address:port to bind the HTTP server")
	cmd.Flags().BoolVar(&skipScanInit, "skip-scan-init", false, "do not pull/probe scanner images at startup")
	return cmd
}

// --- doctor -----------------------------------------------------------------

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the container scanner backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Install backend without forcing a pull (Doctor reports image
			// status; it doesn't need EnsureImage to have succeeded).
			ctx := cmd.Context()
			_ = installScannerBackend(ctx) // best-effort
			return scanners.Doctor(ctx, os.Stdout)
		},
	}
}

// --- pull -------------------------------------------------------------------

func newPullCmd() *cobra.Command {
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Pull configured artifacts",
	}
	pull.AddCommand(&cobra.Command{
		Use:   "scanners",
		Short: "Pre-pull every scanner image in the current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if _, err := scanners.LoadAndInstall(ctx); err != nil {
				// We tolerate the default image pull failing here because
				// scanners.Pull will try each image individually and report.
				wolflog.L().Warn().Err(err).Msg("default image install raised an error; continuing per-image")
			}
			return scanners.Pull(ctx)
		},
	})
	return pull
}

// --- version ----------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version info",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("wolf %s\n  commit:     %s\n  built:      %s\n",
				version, commit, buildDate)
		},
	}
}

// --- scan -------------------------------------------------------------------

func newScanCmd() *cobra.Command {
	var (
		repoPath           string
		branch             string
		tools              []string
		concurrency        int
		heavyConcurrency   int
		networkConcurrency int
		allScanners        bool
		detectOnly         bool
		planOnly           bool
		outDir             string
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Run a one-shot scan against a repository path",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repoPath == "" {
				return fmt.Errorf("--repo is required")
			}
			absRepo, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve --repo: %w", err)
			}
			ctx := cmd.Context()

			// Detect languages/frameworks before installing scanner backend
			// so --detect-only short-circuits the (slower) image-availability check.
			detResult, derr := detector.Detect(absRepo)
			if derr != nil {
				return fmt.Errorf("language detection: %w", derr)
			}
			langs := languagesFromDetection(detResult)
			if planOnly {
				scannerPlan, err := buildLocalScannerPlan(langs, tools, allScanners)
				if err != nil {
					return err
				}
				payload := struct {
					planner.Result
					DetectionSource string   `json:"detection_source"`
					Languages       []string `json:"languages"`
				}{
					Result:          scannerPlan,
					DetectionSource: "local",
					Languages:       langs2strings(langs),
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			fmt.Printf("Detected languages: %v\n", langs)
			if len(detResult.Frameworks) > 0 {
				fmt.Printf("Detected frameworks: %v\n", detResult.Frameworks)
			}
			fmt.Printf("Files: %d total, %d test\n", detResult.TotalFiles, len(detResult.TestFiles))
			if detectOnly {
				return nil
			}

			if err := installScannerBackend(ctx); err != nil {
				return fmt.Errorf("scanner backend: %w", err)
			}

			// Initialize the durable artifact store and reserve a per-scan dir.
			// We use the same layout as `wolf serve` (~/.wolf/artifacts/<id>/)
			// so artifacts are discoverable regardless of entrypoint.
			home, _ := os.UserHomeDir()
			artifactsRoot := outDir
			if artifactsRoot == "" {
				artifactsRoot = filepath.Join(home, ".wolf", "artifacts")
			}
			if err := artifacts.Init(artifactsRoot); err != nil {
				return fmt.Errorf("init artifacts store: %w", err)
			}
			scanID := uuid.New().String()
			// Project- and time-stamped directory so multiple scans of
			// multiple repos from the same host stay easy to distinguish
			// on disk. Falls under the artifacts root rather than the
			// generic <root>/<uuid>/ layout used by the API server.
			scanDirName := report.ScanDirName(absRepo, time.Now().UTC(), scanID)
			scanDir := filepath.Join(artifactsRoot, scanDirName)
			if err := os.MkdirAll(scanDir, 0o750); err != nil {
				return fmt.Errorf("create scan dir: %w", err)
			}
			rawDir := filepath.Join(scanDir, "raw")
			_ = os.MkdirAll(rawDir, 0o750)

			fmt.Printf("Scan ID: %s\nArtifacts: %s\n\n", scanID, scanDir)

			startedAt := time.Now().UTC()
			cfg := runner.RunConfig{
				RepoPath:           absRepo,
				Branch:             branch,
				Registry:           plugin.Global,
				Tools:              tools,
				Concurrency:        concurrency,
				HeavyConcurrency:   heavyConcurrency,
				NetworkConcurrency: networkConcurrency,
				RawOutputDir:       rawDir,
				ContainerCfg:       nil, // shim falls back to container.Default()
				OnToolOutput: func(toolName, line string) {
					fmt.Printf("[%s] %s\n", toolName, line)
				},
			}
			if !allScanners && len(tools) == 0 {
				cfg.Languages = langs
			}

			var scannerPlan *report.ScannerPlan
			if toolManifest, merr := scannermanifest.LoadDefault(); merr == nil {
				cfg.ToolResources = runner.ResourceSpecsFromManifest(toolManifest)
				scannerPlan = planner.ToReportPlan(planner.Build(planner.Config{
					Registry:    plugin.Global,
					Manifest:    toolManifest,
					Languages:   langs,
					Tools:       tools,
					AllScanners: allScanners,
				}))
			}

			result, err := runner.Run(ctx, cfg)
			if err != nil {
				return err
			}
			finishedAt := time.Now().UTC()
			commitSHA := readGitCommit(absRepo)
			sourceRef := branch
			if commitSHA != "" {
				if sourceRef != "" {
					sourceRef += "@" + commitSHA
				} else {
					sourceRef = commitSHA
				}
			}
			for i := range result.Findings {
				if result.Findings[i].SourceKind == "" {
					result.Findings[i].SourceKind = "local_path"
				}
				if result.Findings[i].SourceRef == "" {
					result.Findings[i].SourceRef = sourceRef
				}
			}

			// Build report + manifest and persist all artifacts.
			rcfg := report.ReportConfig{
				ScanID:      scanID,
				RepoName:    filepath.Base(absRepo),
				Branch:      branch,
				Findings:    result.Findings,
				Languages:   languageCountsAsString(detResult.Languages),
				Frameworks:  detResult.Frameworks,
				ToolsRun:    result.ToolsRun,
				ToolsFailed: result.ToolsFailed,
				Duration:    result.Duration,
			}
			manifest := report.Manifest{
				ScanID:      scanID,
				Source:      localSourceProvenance(absRepo, branch, commitSHA),
				RepoName:    filepath.Base(absRepo),
				RepoPath:    absRepo,
				RepoCommit:  commitSHA,
				Branch:      branch,
				StartedAt:   startedAt,
				FinishedAt:  finishedAt,
				WolfVersion: version,
				Detection: report.DetectionSummary{
					Languages:  langs2strings(langs),
					Frameworks: detResult.Frameworks,
					TestFiles:  len(detResult.TestFiles),
					TotalFiles: detResult.TotalFiles,
				},
				ScannersRun: result.ToolsRun,
				Skipped:     skippedFrom(result),
				ScannerPlan: scannerPlan,
				Failed:      failedFrom(result.ToolsFailed),
				Counts:      report.CountFindings(0, result.Findings),
			}
			written, werr := report.WriteAll(scanDir, rcfg, manifest)
			if werr != nil {
				fmt.Fprintf(os.Stderr, "warning: artifact write failed: %v\n", werr)
			}

			fmt.Printf("\nScan complete: %d findings across %d tools in %s\n",
				len(result.Findings), len(result.ToolsRun), result.Duration)
			fmt.Println("Artifacts written:")
			if written.FixHigh != "" {
				fmt.Printf("  FIX-HIGH.md     %s\n", written.FixHigh)
			}
			if written.FixAll != "" {
				fmt.Printf("  FIX-ALL.md      %s\n", written.FixAll)
			}
			if written.FindingsJSON != "" {
				fmt.Printf("  findings.json   %s\n", written.FindingsJSON)
			}
			if written.RawMarkdown != "" {
				fmt.Printf("  RAW.md          %s\n", written.RawMarkdown)
			}
			if written.CombinedSARIF != "" {
				fmt.Printf("  combined.sarif  %s\n", written.CombinedSARIF)
			}
			if written.Manifest != "" {
				fmt.Printf("  manifest.json   %s\n", written.Manifest)
			}
			fmt.Printf("  raw/            %s\n", rawDir)
			if len(result.ToolsFailed) > 0 {
				fmt.Printf("\n%d tool(s) failed:\n", len(result.ToolsFailed))
				for tool, e := range result.ToolsFailed {
					fmt.Printf("  %s: %s\n", tool, e)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", "", "repository path (required)")
	cmd.Flags().StringVar(&branch, "branch", "main", "branch label for the scan record")
	cmd.Flags().StringSliceVar(&tools, "tools", nil, "explicit tool list (default: auto-detect by language)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "parallel tool execution cap")
	cmd.Flags().IntVar(&heavyConcurrency, "heavy-concurrency", 1, "parallel heavy scanner execution cap")
	cmd.Flags().IntVar(&networkConcurrency, "network-concurrency", 2, "parallel network scanner execution cap")
	cmd.Flags().BoolVar(&allScanners, "all-scanners", false, "run every registered scanner regardless of detected languages")
	cmd.Flags().BoolVar(&detectOnly, "detect-only", false, "report detected languages/frameworks and exit without scanning")
	cmd.Flags().BoolVar(&planOnly, "plan-only", false, "emit scanner run/skip plan and exit without scanning")
	cmd.Flags().StringVar(&outDir, "out", "", "artifacts root directory (default ~/.wolf/artifacts)")
	return cmd
}

func buildLocalScannerPlan(langs []models.Language, tools []string, allScanners bool) (planner.Result, error) {
	toolManifest, err := scannermanifest.LoadDefault()
	if err != nil {
		return planner.Result{}, err
	}
	return planner.Build(planner.Config{
		Registry:    plugin.Global,
		Manifest:    toolManifest,
		Languages:   langs,
		Tools:       tools,
		AllScanners: allScanners,
	}), nil
}

// languagesFromDetection extracts the non-zero language keys from a
// detector.DetectionResult, in a deterministic order (by file-count desc, then
// name asc). This is what feeds runner.RunConfig.Languages.
func languagesFromDetection(d *detector.DetectionResult) []models.Language {
	if d == nil || len(d.Languages) == 0 {
		return nil
	}
	type kv struct {
		lang  models.Language
		count int
	}
	pairs := make([]kv, 0, len(d.Languages))
	for l, c := range d.Languages {
		if c > 0 {
			pairs = append(pairs, kv{l, c})
		}
	}
	// Stable order: descending count, ascending name.
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].count > pairs[i].count ||
				(pairs[j].count == pairs[i].count && string(pairs[j].lang) < string(pairs[i].lang)) {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	out := make([]models.Language, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.lang)
	}
	return out
}

func langs2strings(langs []models.Language) []string {
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		out = append(out, string(l))
	}
	return out
}

func languageCountsAsString(in map[models.Language]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}

// skippedFrom converts the runner's untyped skipped-tool list (just names,
// no reasons) into ScannerSkip records. The runner only knows tools that
// failed CheckAvailable, so the reason is always "unavailable".
func skippedFrom(r *runner.RunResult) []report.ScannerSkip {
	if r == nil {
		return nil
	}
	out := make([]report.ScannerSkip, 0, len(r.ToolsSkipped))
	for _, t := range r.ToolsSkipped {
		out = append(out, report.ScannerSkip{Tool: t, Reason: "unavailable"})
	}
	return out
}

func failedFrom(in map[string]error) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v.Error()
	}
	return out
}

// readGitCommit best-efforts the HEAD short SHA for the manifest. Falls back
// to "" when repoPath isn't a git checkout — the manifest field is omitempty.
func readGitCommit(repoPath string) string {
	gitHead := filepath.Join(repoPath, ".git", "HEAD")
	// #nosec G304 -- path is an artifact under the scan dir
	data, err := os.ReadFile(gitHead)
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if !strings.HasPrefix(head, "ref: ") {
		// detached HEAD
		if len(head) >= 7 {
			return head[:7]
		}
		return head
	}
	ref := strings.TrimPrefix(head, "ref: ")
	// #nosec G304 -- path is an artifact under the scan dir
	refData, err := os.ReadFile(filepath.Join(repoPath, ".git", ref))
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(refData))
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

// --- enrich -----------------------------------------------------------------

// newEnrichCmd builds `wolf enrich` — writes AI-ready remediation prompts
// (`ai_fix_prompt`) into a scan's findings.json for the selected findings.
func newEnrichCmd() *cobra.Command {
	var (
		findingsPath string
		scanID       string
		severities   []string
		categories   []string
		tools        []string
		excludePaths []string
		ids          []string
		useAI        bool
	)
	cmd := &cobra.Command{
		Use:   "enrich",
		Short: "Write AI-ready fix prompts into a scan's findings.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveFindingsPath(findingsPath, scanID)
			if err != nil {
				return err
			}

			doc, err := enrich.LoadDoc(path)
			if err != nil {
				return err
			}

			filter := enrich.Filter{
				Severities:   severities,
				Categories:   categories,
				Tools:        tools,
				IDs:          ids,
				ExcludePaths: excludePaths,
			}

			var gen enrich.Generator = enrich.TemplateGenerator{}
			if useAI {
				aiGen, gerr := enrich.NewAIGenerator()
				if gerr != nil {
					return fmt.Errorf("--ai requested but unavailable: %w", gerr)
				}
				gen = aiGen
			}

			n, err := doc.Apply(filter, gen)
			if err != nil {
				return err
			}
			if err := doc.Write(path); err != nil {
				return err
			}
			fmt.Printf("Enriched %d of %d findings in %s\n", n, doc.FindingCount(), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&findingsPath, "findings", "", "path to a findings.json artifact")
	cmd.Flags().StringVar(&scanID, "scan", "", "scan ID (resolves <artifacts-root>/<id>/findings.json)")
	cmd.Flags().StringSliceVar(&severities, "severity", nil, "only enrich these severities (critical,high,...)")
	cmd.Flags().StringSliceVar(&categories, "category", nil, "only enrich these categories (sast,sca,secrets,...)")
	cmd.Flags().StringSliceVar(&tools, "tool", nil, "only enrich findings from these tools")
	cmd.Flags().StringSliceVar(&excludePaths, "exclude-path", nil, "skip findings whose file path matches these globs")
	cmd.Flags().StringSliceVar(&ids, "ids", nil, "only enrich these specific finding IDs")
	cmd.Flags().BoolVar(&useAI, "ai", false, "use the configured AI provider for richer guidance")
	return cmd
}

// resolveFindingsPath turns a --findings path or a --scan ID into a
// concrete findings.json path.
func resolveFindingsPath(findingsPath, scanID string) (string, error) {
	if findingsPath != "" {
		if _, err := os.Stat(findingsPath); err != nil {
			return "", fmt.Errorf("findings file not found: %s", findingsPath)
		}
		return findingsPath, nil
	}
	if scanID == "" {
		return "", fmt.Errorf("one of --findings or --scan is required")
	}
	home, _ := os.UserHomeDir()
	root := envOr("WOLF_ARTIFACTS_ROOT", filepath.Join(home, ".wolf", "artifacts"))
	candidate := filepath.Join(root, scanID, "findings.json")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("findings.json for scan %s not found at %s — pass --findings <path> instead", scanID, candidate)
	}
	return candidate, nil
}

// --- loop -------------------------------------------------------------------

// newLoopCmd builds `wolf loop` — runs the scan -> AI fix -> rescan
// auto-remediation loop against a git repository.
func newLoopCmd() *cobra.Command {
	var (
		repoPath      string
		branch        string
		maxIterations int
		aiTool        string
		severities    []string
		fixTimeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "loop",
		Short: "Run the AI auto-remediation loop (scan -> fix -> rescan)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repoPath == "" {
				return fmt.Errorf("--repo is required")
			}
			absRepo, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve --repo: %w", err)
			}
			// The loop requires git — fail fast with an actionable error.
			if _, err := os.Stat(filepath.Join(absRepo, ".git")); err != nil {
				return fmt.Errorf("%s is not a git repository — run `git init` or add it as a git source; scan and enrich still work on non-git paths", absRepo)
			}

			eng, err := engine.NewEngine(aiTool)
			if err != nil {
				return err
			}
			if !eng.Available() {
				return fmt.Errorf("AI tool %q is not available on PATH", eng.Name())
			}

			ctx := cmd.Context()
			if err := installScannerBackend(ctx); err != nil {
				return fmt.Errorf("scanner backend: %w", err)
			}

			sevs := make([]models.Severity, 0, len(severities))
			for _, s := range severities {
				sevs = append(sevs, models.Severity(strings.ToLower(strings.TrimSpace(s))))
			}

			cfg := controller.Config{
				RepoPath:      absRepo,
				MaxIterations: maxIterations,
				Severities:    sevs,
				FixEngine:     eng,
				FixTimeout:    fixTimeout,
				ScanConfig: runner.RunConfig{
					RepoPath: absRepo,
					Branch:   branch,
					Registry: plugin.Global,
				},
				OnIterationStart: func(it int) {
					fmt.Printf("\n=== iteration %d ===\n", it)
				},
				OnIterationDone: func(it int, diff *tracker.IterationDiff, warnings []string) {
					if diff != nil {
						fmt.Printf("iteration %d: %d fixed, %d new, %d remaining\n",
							it, diff.FixedCount, diff.NewCount, diff.RemainingCount)
					}
					for _, w := range warnings {
						fmt.Printf("  ! %s\n", w)
					}
				},
			}

			fmt.Printf("Starting loop: repo=%s tool=%s max-iterations=%d\n",
				absRepo, eng.Name(), cfg.MaxIterations)
			loop, err := controller.New(cfg).Run(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("\nLoop %s: status=%s  fixed=%d  remaining=%d\n",
				loop.ID, loop.Status, loop.TotalFindingsFixed, loop.TotalFindingsRemaining)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", "", "path to the git repository (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "branch to scan (defaults to the checked-out branch)")
	cmd.Flags().IntVar(&maxIterations, "max-iterations", 5, "maximum loop iterations")
	cmd.Flags().StringVar(&aiTool, "ai-tool", "auto", "AI fix engine (auto, claude-code, codex, custom:<cmd>)")
	cmd.Flags().StringSliceVar(&severities, "severity", nil, "only target these severities")
	cmd.Flags().DurationVar(&fixTimeout, "fix-timeout", 5*time.Minute, "per-finding fix timeout")
	return cmd
}

// localSourceProvenance describes a scan of an on-disk working tree —
// captured in the manifest so audits can tie findings back to a specific
// repository state.
func localSourceProvenance(repoPath, branch, commitSHA string) *report.SourceProvenance {
	return &report.SourceProvenance{
		Kind:             "local_path",
		RepoPath:         repoPath,
		Branch:           branch,
		CommitSHA:        commitSHA,
		SnapshotStrategy: "working_tree",
	}
}

// --- helpers ----------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
