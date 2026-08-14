package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// AddScanSubcommands attaches the API-client scan subcommands to the
// existing local `scan` command. After this, `wolf scan` (with --repo) runs
// a local one-shot scan, while `wolf scan list`, `wolf scan create`, etc.
// drive the server's scan API.
func AddScanSubcommands(scan *cobra.Command) {
	var repoID, collectionID, branch string
	var tools []string
	var aiEnabled bool
	create := &cobra.Command{
		Use:         "create",
		Short:       "Start a server-managed scan",
		Annotations: apiAnno("POST", "/scans"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repoID == "" {
				return fmt.Errorf("--repo is required")
			}
			body := map[string]any{"repo_id": repoID}
			if collectionID != "" {
				body["collection_id"] = collectionID
			}
			if branch != "" {
				body["branch"] = branch
			}
			if len(tools) > 0 {
				body["tools"] = tools
			}
			if cmd.Flags().Changed("ai") {
				body["ai_enabled"] = aiEnabled
			}
			return runRender(cmd, "POST", "/scans", body)
		},
	}
	create.Flags().StringVar(&repoID, "repo", "", "repository ID")
	create.Flags().StringVar(&collectionID, "collection", "", "collection ID")
	create.Flags().StringVar(&branch, "branch", "", "branch to scan")
	create.Flags().StringSliceVar(&tools, "tools", nil, "explicit tool list")
	create.Flags().BoolVar(&aiEnabled, "ai", false, "enable AI enrichment")

	var rescanRelease, rescanReason, rescanKey string
	releaseRescan := &cobra.Command{
		Use:         "rescan-release <id>",
		Short:       "Create a distinct scan pinned to a newly selected scanner release",
		Annotations: apiAnno("POST", "/scans/{}/release-rescans"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rescanRelease == "" || rescanReason == "" {
				return fmt.Errorf("--release and --reason are required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				"/scans/"+url.PathEscape(args[0])+"/release-rescans",
				map[string]any{"release_id": rescanRelease, "reason": rescanReason},
				rescanKey, "", false)
		},
	}
	releaseRescan.Flags().StringVar(&rescanRelease, "release", "", "immutable scanner release ID")
	releaseRescan.Flags().StringVar(&rescanReason, "reason", "", "auditable reason for changing release")
	releaseRescan.Flags().StringVar(&rescanKey, "idempotency-key", "", "stable command key")

	compare := &cobra.Command{
		Use:         "compare <id> <other-id>",
		Annotations: apiAnno("GET", "/scans/{}/compare/{}"),
		Short:       "Compare two scans",
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", "/scans/"+args[0]+"/compare/"+args[1], nil)
		},
	}

	toolOutput := &cobra.Command{
		Use:         "tool-output <id> <tool>",
		Annotations: apiAnno("GET", "/scans/{}/tools/{}/output"),
		Short:       "Get a tool's raw output for a scan",
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", "/scans/"+args[0]+"/tools/"+args[1]+"/output", nil)
		},
	}

	cancelTool := &cobra.Command{
		Use:         "cancel-tool <id> <tool>",
		Annotations: apiAnno("DELETE", "/scans/{}/tools/{}"),
		Short:       "Cancel one tool of a running scan",
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "DELETE", "/scans/"+args[0]+"/tools/"+args[1], nil)
		},
	}

	var trendRepo, trendBranch string
	var trendLimit int
	trends := &cobra.Command{
		Use:         "trends",
		Short:       "Scan trends over time for a repository",
		Annotations: apiAnno("GET", "/scans/trends"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if trendRepo == "" {
				return fmt.Errorf("--repo is required")
			}
			q := url.Values{}
			q.Set("repo_id", trendRepo)
			if trendBranch != "" {
				q.Set("branch", trendBranch)
			}
			if cmd.Flags().Changed("limit") {
				q.Set("limit", strconv.Itoa(trendLimit))
			}
			return runRender(cmd, "GET", "/scans/trends?"+q.Encode(), nil)
		},
	}
	trends.Flags().StringVar(&trendRepo, "repo", "", "repository ID (required)")
	trends.Flags().StringVar(&trendBranch, "branch", "", "branch filter")
	trends.Flags().IntVar(&trendLimit, "limit", 30, "max data points")

	var gateFailExitCode bool
	gate := &cobra.Command{
		Use:         "gate <id>",
		Annotations: apiAnno("GET", "/scans/{}/gate"),
		Short:       "Evaluate a scan's quality gate",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			env, err := c.Do(cmd.Context(), "GET", "/scans/"+args[0]+"/gate", nil)
			if err != nil {
				return err
			}
			if err := Render(cmd.OutOrStdout(), resolveOutput(cmd), env); err != nil {
				return err
			}
			if !gateFailExitCode {
				return nil
			}
			var data struct {
				Evaluation struct {
					Status string `json:"status"`
				} `json:"evaluation"`
				Result struct {
					Status string `json:"status"`
				} `json:"result"`
			}
			_ = json.Unmarshal(env.Data, &data)
			status := data.Evaluation.Status
			if status == "" {
				status = data.Result.Status
			}
			if status == "fail" {
				return &GateFailedError{ScanID: args[0], Status: status}
			}
			return nil
		},
	}
	gate.Flags().BoolVar(&gateFailExitCode, "fail-exit-code", false, "return exit code 2 when the quality gate fails")

	var preflightRepo, preflightBranch string
	preflight := &cobra.Command{
		Use:         "preflight",
		Short:       "Preview which tools a scan would run",
		Annotations: apiAnno("POST", "/scans/preflight"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if preflightRepo == "" {
				return fmt.Errorf("--repo is required")
			}
			body := map[string]any{"repo_id": preflightRepo}
			if preflightBranch != "" {
				body["branch"] = preflightBranch
			}
			return runRender(cmd, "POST", "/scans/preflight", body)
		},
	}
	preflight.Flags().StringVar(&preflightRepo, "repo", "", "repository ID")
	preflight.Flags().StringVar(&preflightBranch, "branch", "", "branch to preflight")

	downloadArtifact := &cobra.Command{
		Use:         "download-artifact <id> <artifact-id>",
		Short:       "Download a scan artifact to stdout",
		Annotations: apiAnno("GET", "/scans/{}/artifacts/{}/download"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			raw, err := c.Raw(cmd.Context(), "/scans/"+args[0]+"/artifacts/"+args[1]+"/download")
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		},
	}

	orphans := &cobra.Command{
		Use:         "orphans",
		Short:       "List leftover scans whose repo was deleted",
		Annotations: apiAnno("GET", "/scans/orphans"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd, "GET", "/scans/orphans", nil)
		},
	}
	purgeOrphans := &cobra.Command{
		Use:         "purge-orphans",
		Short:       "Delete leftover scans and findings for missing repos",
		Annotations: apiAnno("DELETE", "/scans/orphans"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd, "DELETE", "/scans/orphans", nil)
		},
	}

	scan.AddCommand(
		listCmd("/scans", "List scans"),
		getCmd("/scans", "Get a scan"),
		orphans,
		purgeOrphans,
		create,
		releaseRescan,
		preflight,
		deleteCmd("cancel <id>", "Cancel a scan", "/scans/%s"),
		cancelTool,
		subGetCmd("diff <id>", "Diff a scan against its baseline", "/scans/%s/diff"),
		downloadArtifact,
		watchCmd("Stream a scan's progress", "/scans/%s/stream"),
		subGetCmd("findings <id>", "List a scan's findings", "/scans/%s/findings"),
		subGetCmd("stats <id>", "Finding statistics for a scan", "/scans/%s/findings/stats"),
		subGetCmd("report <id>", "Get a scan's report", "/scans/%s/report"),
		subGetCmd("result <id>", "Get the stable automation result for a scan", "/scans/%s/result"),
		subGetCmd("manifest <id>", "Get a scan's manifest", "/scans/%s/manifest"),
		subGetCmd("sarif <id>", "Get a scan's SARIF output", "/scans/%s/sarif"),
		subGetCmd("coverage <id>", "Get a scan's coverage", "/scans/%s/coverage"),
		gate,
		subGetCmd("tools <id>", "List a scan's tools", "/scans/%s/tools"),
		subGetCmd("scanner-runs <id>", "List scanner run records", "/scans/%s/scanner-runs"),
		subGetCmd("ai-logs <id>", "List a scan's AI logs", "/scans/%s/ai-logs"),
		subGetCmd("tool-summaries <id>", "List a scan's tool summaries", "/scans/%s/tool-summaries"),
		subGetCmd("recommendations <id>", "List a scan's recommendations", "/scans/%s/recommendations"),
		subGetCmd("lineage <id>", "Show origin-scan lineage (children and agents)", "/scans/%s/lineage"),
		compare,
		toolOutput,
		trends,
	)
}
