package cli

import (
	"fmt"

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
		subGetCmd("sarif <id>", "Get a scan's SARIF output", "/scans/%s/sarif"),
		subGetCmd("coverage <id>", "Get a scan's coverage", "/scans/%s/coverage"),
		subGetCmd("tools <id>", "List a scan's tools", "/scans/%s/tools"),
		subGetCmd("ai-logs <id>", "List a scan's AI logs", "/scans/%s/ai-logs"),
		subGetCmd("tool-summaries <id>", "List a scan's tool summaries", "/scans/%s/tool-summaries"),
		subGetCmd("recommendations <id>", "List a scan's recommendations", "/scans/%s/recommendations"),
		compare,
		toolOutput,
		&cobra.Command{
			Use: "trends", Short: "Scan trends over time", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/scans/trends", nil) },
		},
	)
}
