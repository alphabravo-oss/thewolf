package cli

import (
	"encoding/json"
	"fmt"
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
		Use:   "create",
		Short: "Start a server-managed scan",
		Args:  cobra.NoArgs,
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

	compare := &cobra.Command{
		Use:   "compare <id> <other-id>",
		Short: "Compare two scans",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", "/scans/"+args[0]+"/compare/"+args[1], nil)
		},
	}

	toolOutput := &cobra.Command{
		Use:   "tool-output <id> <tool>",
		Short: "Get a tool's raw output for a scan",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", "/scans/"+args[0]+"/tools/"+args[1]+"/output", nil)
		},
	}

	cancelTool := &cobra.Command{
		Use:   "cancel-tool <id> <tool>",
		Short: "Cancel one tool of a running scan",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "DELETE", "/scans/"+args[0]+"/tools/"+args[1], nil)
		},
	}

	var trendRepo, trendBranch string
	var trendLimit int
	trends := &cobra.Command{
		Use:   "trends",
		Short: "Scan trends over time for a repository",
		Args:  cobra.NoArgs,
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
		Use:   "gate <id>",
		Short: "Evaluate a scan's quality gate",
		Args:  cobra.ExactArgs(1),
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

	scan.AddCommand(
		listCmd("/scans", "List scans"),
		getCmd("/scans", "Get a scan"),
		create,
		deleteCmd("cancel <id>", "Cancel a scan", "/scans/%s"),
		cancelTool,
		watchCmd("Stream a scan's progress", "/scans/%s/stream"),
		subGetCmd("findings <id>", "List a scan's findings", "/scans/%s/findings"),
		subGetCmd("stats <id>", "Finding statistics for a scan", "/scans/%s/findings/stats"),
		subGetCmd("report <id>", "Get a scan's report", "/scans/%s/report"),
		subGetCmd("manifest <id>", "Get a scan's manifest", "/scans/%s/manifest"),
		subGetCmd("sarif <id>", "Get a scan's SARIF output", "/scans/%s/sarif"),
		subGetCmd("coverage <id>", "Get a scan's coverage", "/scans/%s/coverage"),
		gate,
		subGetCmd("tools <id>", "List a scan's tools", "/scans/%s/tools"),
		subGetCmd("scanner-runs <id>", "List scanner run records", "/scans/%s/scanner-runs"),
		subGetCmd("ai-logs <id>", "List a scan's AI logs", "/scans/%s/ai-logs"),
		subGetCmd("tool-summaries <id>", "List a scan's tool summaries", "/scans/%s/tool-summaries"),
		subGetCmd("recommendations <id>", "List a scan's recommendations", "/scans/%s/recommendations"),
		compare,
		toolOutput,
		trends,
	)
}
