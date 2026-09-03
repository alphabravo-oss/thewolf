package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newScheduleCmd() *cobra.Command {
	cmd := group("schedule", "Manage recurring scan schedules")

	var repoID, collectionID, branch, profile, quietStart, quietEnd string
	var interval int
	var enabled bool
	create := &cobra.Command{
		Use:         "create",
		Short:       "Create a scan schedule",
		Annotations: apiAnno("POST", "/schedules"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (repoID == "") == (collectionID == "") {
				return fmt.Errorf("exactly one of --repo or --collection is required")
			}
			if interval == 0 {
				return fmt.Errorf("--interval is required")
			}
			body := map[string]any{"interval_minutes": interval, "enabled": true}
			if cmd.Flags().Changed("enabled") {
				body["enabled"] = enabled
			}
			if repoID != "" {
				body["repo_id"] = repoID
			}
			if collectionID != "" {
				body["collection_id"] = collectionID
			}
			if branch != "" {
				body["branch"] = branch
			}
			if profile != "" {
				body["profile"] = profile
			}
			if quietStart != "" {
				body["quiet_start"] = quietStart
			}
			if quietEnd != "" {
				body["quiet_end"] = quietEnd
			}
			return runRender(cmd, "POST", "/schedules", body)
		},
	}
	create.Flags().StringVar(&repoID, "repo", "", "repository ID")
	create.Flags().StringVar(&collectionID, "collection", "", "collection ID")
	create.Flags().IntVar(&interval, "interval", 0, "interval minutes: 15, 60, 360, or 1440")
	create.Flags().StringVar(&branch, "branch", "", "branch")
	create.Flags().StringVar(&profile, "profile", "", "scan profile")
	create.Flags().StringVar(&quietStart, "quiet-start", "", "quiet hours start HH:MM")
	create.Flags().StringVar(&quietEnd, "quiet-end", "", "quiet hours end HH:MM")
	create.Flags().BoolVar(&enabled, "enabled", true, "whether the schedule is enabled")

	var upRepo, upCol, upBranch, upProfile, upQuietStart, upQuietEnd string
	var upInterval int
	var upEnabled bool
	update := &cobra.Command{
		Use:         "update <id>",
		Short:       "Update a scan schedule",
		Annotations: apiAnno("PUT", "/schedules/{}"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if cmd.Flags().Changed("repo") {
				body["repo_id"] = upRepo
			}
			if cmd.Flags().Changed("collection") {
				body["collection_id"] = upCol
			}
			if cmd.Flags().Changed("interval") {
				body["interval_minutes"] = upInterval
			}
			if cmd.Flags().Changed("branch") {
				body["branch"] = upBranch
			}
			if cmd.Flags().Changed("profile") {
				body["profile"] = upProfile
			}
			if cmd.Flags().Changed("quiet-start") {
				body["quiet_start"] = upQuietStart
			}
			if cmd.Flags().Changed("quiet-end") {
				body["quiet_end"] = upQuietEnd
			}
			if cmd.Flags().Changed("enabled") {
				body["enabled"] = upEnabled
			}
			return runRender(cmd, "PUT", "/schedules/"+args[0], body)
		},
	}
	update.Flags().StringVar(&upRepo, "repo", "", "repository ID")
	update.Flags().StringVar(&upCol, "collection", "", "collection ID")
	update.Flags().IntVar(&upInterval, "interval", 0, "interval minutes")
	update.Flags().StringVar(&upBranch, "branch", "", "branch")
	update.Flags().StringVar(&upProfile, "profile", "", "scan profile")
	update.Flags().StringVar(&upQuietStart, "quiet-start", "", "quiet hours start HH:MM")
	update.Flags().StringVar(&upQuietEnd, "quiet-end", "", "quiet hours end HH:MM")
	update.Flags().BoolVar(&upEnabled, "enabled", true, "whether the schedule is enabled")

	cmd.AddCommand(
		listCmd("/schedules", "List scan schedules"),
		create,
		update,
		deleteCmd("delete <id>", "Delete a scan schedule", "/schedules/%s"),
	)
	return cmd
}

func newSetupCmd() *cobra.Command {
	cmd := group("setup", "First-run setup")
	cmd.AddCommand(
		&cobra.Command{
			Use: "status", Short: "Show first-run setup status",
			Annotations: apiAnno("GET", "/setup/status"), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/setup/status", nil) },
		},
		&cobra.Command{
			Use: "sample-repo", Short: "Create the sample repository",
			Annotations: apiAnno("POST", "/setup/sample-repo"), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runRender(cmd, "POST", "/setup/sample-repo", nil)
			},
		},
	)
	return cmd
}

func newNotificationCmd() *cobra.Command {
	cmd := group("notification", "In-app notifications")
	cmd.AddCommand(listCmd("/notifications", "List recent notifications"))
	return cmd
}
