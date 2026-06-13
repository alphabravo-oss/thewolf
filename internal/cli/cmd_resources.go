package cli

import (
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

// --- repos ------------------------------------------------------------------

func newRepoCmd() *cobra.Command {
	cmd := group("repo", "Manage repositories")

	var name, sourceType, sourcePath, branch, remoteNodeID, remotePath string
	create := &cobra.Command{
		Use:   "create",
		Short: "Add a repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || sourcePath == "" {
				return fmt.Errorf("--name and --path are required")
			}
			body := map[string]any{"name": name, "source_path": sourcePath}
			if sourceType != "" {
				body["source_type"] = sourceType
			}
			if remoteNodeID != "" {
				body["remote_node_id"] = remoteNodeID
			}
			if remotePath != "" {
				body["remote_path"] = remotePath
			}
			if branch != "" {
				body["default_branch"] = branch
			}
			return runRender(cmd, "POST", "/repos", body)
		},
	}
	create.Flags().StringVar(&name, "name", "", "repository name")
	create.Flags().StringVar(&sourceType, "type", "", "source type (e.g. local, git)")
	create.Flags().StringVar(&sourcePath, "path", "", "source path or URL")
	create.Flags().StringVar(&branch, "branch", "", "default branch")
	create.Flags().StringVar(&remoteNodeID, "node", "", "remote SSH node ID for --type ssh")
	create.Flags().StringVar(&remotePath, "remote-path", "", "remote repo path for --type ssh")

	var upName, upBranch string
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = upName
			}
			if cmd.Flags().Changed("branch") {
				body["default_branch"] = upBranch
			}
			return runRender(cmd, "PUT", "/repos/"+args[0], body)
		},
	}
	update.Flags().StringVar(&upName, "name", "", "new name")
	update.Flags().StringVar(&upBranch, "branch", "", "new default branch")

	cmd.AddCommand(
		listCmd("/repos", "List repositories"),
		getCmd("/repos", "Get a repository"),
		create,
		update,
		deleteCmd("delete <id>", "Delete a repository", "/repos/%s"),
		subGetCmd("branches <id>", "List a repository's branches", "/repos/%s/branches"),
	)
	return cmd
}

// --- remote nodes -----------------------------------------------------------

func newNodeCmd() *cobra.Command {
	cmd := group("node", "Manage remote SSH nodes")

	var name, host, username, authType, secretID, knownHosts, basePath string
	var port int
	var enabled bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Add a remote SSH node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || host == "" || username == "" {
				return fmt.Errorf("--name, --host, and --username are required")
			}
			body := map[string]any{
				"name": name, "host": host, "username": username, "enabled": enabled,
			}
			if port != 0 {
				body["port"] = port
			}
			if authType != "" {
				body["auth_type"] = authType
			}
			if secretID != "" {
				body["credential_secret_id"] = secretID
			}
			if knownHosts != "" {
				body["known_hosts"] = knownHosts
			}
			if basePath != "" {
				body["base_path"] = basePath
			}
			return runRender(cmd, "POST", "/nodes", body)
		},
	}
	create.Flags().StringVar(&name, "name", "", "node name")
	create.Flags().StringVar(&host, "host", "", "SSH host")
	create.Flags().IntVar(&port, "port", 22, "SSH port")
	create.Flags().StringVar(&username, "username", "", "SSH username")
	create.Flags().StringVar(&authType, "auth", "private_key", "SSH auth type: private_key or password")
	create.Flags().StringVar(&secretID, "secret", "", "credential secret ID")
	create.Flags().StringVar(&knownHosts, "known-hosts", "", "known_hosts line(s) for host-key verification")
	create.Flags().StringVar(&basePath, "base-path", "", "default remote browse root")
	create.Flags().BoolVar(&enabled, "enabled", true, "node enabled")

	var upName, upHost, upUsername, upAuthType, upSecretID, upKnownHosts, upBasePath string
	var upPort int
	var upEnabled bool
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a remote SSH node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = upName
			}
			if cmd.Flags().Changed("host") {
				body["host"] = upHost
			}
			if cmd.Flags().Changed("port") {
				body["port"] = upPort
			}
			if cmd.Flags().Changed("username") {
				body["username"] = upUsername
			}
			if cmd.Flags().Changed("auth") {
				body["auth_type"] = upAuthType
			}
			if cmd.Flags().Changed("secret") {
				body["credential_secret_id"] = upSecretID
			}
			if cmd.Flags().Changed("known-hosts") {
				body["known_hosts"] = upKnownHosts
			}
			if cmd.Flags().Changed("base-path") {
				body["base_path"] = upBasePath
			}
			if cmd.Flags().Changed("enabled") {
				body["enabled"] = upEnabled
			}
			return runRender(cmd, "PUT", "/nodes/"+args[0], body)
		},
	}
	update.Flags().StringVar(&upName, "name", "", "node name")
	update.Flags().StringVar(&upHost, "host", "", "SSH host")
	update.Flags().IntVar(&upPort, "port", 0, "SSH port")
	update.Flags().StringVar(&upUsername, "username", "", "SSH username")
	update.Flags().StringVar(&upAuthType, "auth", "", "SSH auth type: private_key or password")
	update.Flags().StringVar(&upSecretID, "secret", "", "credential secret ID")
	update.Flags().StringVar(&upKnownHosts, "known-hosts", "", "known_hosts line(s)")
	update.Flags().StringVar(&upBasePath, "base-path", "", "default remote browse root")
	update.Flags().BoolVar(&upEnabled, "enabled", true, "node enabled")

	browse := &cobra.Command{
		Use:   "browse <id> [path]",
		Short: "Browse a remote SSH node",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if len(args) == 2 {
				q.Set("path", args[1])
			}
			suffix := ""
			if encoded := q.Encode(); encoded != "" {
				suffix = "?" + encoded
			}
			return runRender(cmd, "GET", "/nodes/"+args[0]+"/browse"+suffix, nil)
		},
	}
	gitInfo := &cobra.Command{
		Use:   "git-info <id> <path>",
		Short: "Inspect a remote git working tree",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			q.Set("path", args[1])
			return runRender(cmd, "GET", "/nodes/"+args[0]+"/git-info?"+q.Encode(), nil)
		},
	}

	cmd.AddCommand(
		listCmd("/nodes", "List remote SSH nodes"),
		getCmd("/nodes", "Get a remote SSH node"),
		create,
		update,
		deleteCmd("delete <id>", "Delete a remote SSH node", "/nodes/%s"),
		&cobra.Command{Use: "check <id>", Short: "Check SSH connectivity", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, "POST", "/nodes/"+args[0]+"/check", nil)
			}},
		browse,
		gitInfo,
	)
	return cmd
}

// --- collections ------------------------------------------------------------

func newCollectionCmd() *cobra.Command {
	cmd := group("collection", "Manage collections of repositories")

	var name, desc string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a collection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return runRender(cmd, "POST", "/collections", map[string]any{
				"name": name, "description": desc,
			})
		},
	}
	create.Flags().StringVar(&name, "name", "", "collection name")
	create.Flags().StringVar(&desc, "description", "", "collection description")

	var upName, upDesc string
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = upName
			}
			if cmd.Flags().Changed("description") {
				body["description"] = upDesc
			}
			return runRender(cmd, "PUT", "/collections/"+args[0], body)
		},
	}
	update.Flags().StringVar(&upName, "name", "", "new name")
	update.Flags().StringVar(&upDesc, "description", "", "new description")

	var addRepoID, rmRepoID string
	addRepo := &cobra.Command{
		Use:   "add-repo <id>",
		Short: "Add a repository to a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if addRepoID == "" {
				return fmt.Errorf("--repo is required")
			}
			return runRender(cmd, "POST", "/collections/"+args[0]+"/repos", map[string]any{"repo_id": addRepoID})
		},
	}
	addRepo.Flags().StringVar(&addRepoID, "repo", "", "repository ID to add")

	removeRepo := &cobra.Command{
		Use:   "remove-repo <id>",
		Short: "Remove a repository from a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rmRepoID == "" {
				return fmt.Errorf("--repo is required")
			}
			return runRender(cmd, "DELETE", "/collections/"+args[0]+"/repos/"+rmRepoID, nil)
		},
	}
	removeRepo.Flags().StringVar(&rmRepoID, "repo", "", "repository ID to remove")

	cmd.AddCommand(
		listCmd("/collections", "List collections"),
		getCmd("/collections", "Get a collection"),
		create,
		update,
		deleteCmd("delete <id>", "Delete a collection", "/collections/%s"),
		addRepo,
		removeRepo,
		subGetCmd("tools <id>", "List a collection's tools", "/collections/%s/tools"),
		subGetCmd("metrics <id>", "Get a collection's metrics", "/collections/%s/metrics"),
	)
	return cmd
}

// --- baselines --------------------------------------------------------------

func newBaselineCmd() *cobra.Command {
	cmd := group("baseline", "Manage scan baselines")

	var listRepo, listBranch string
	list := &cobra.Command{
		Use:   "list",
		Short: "List repository baselines",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if listRepo == "" {
				return fmt.Errorf("--repo is required")
			}
			q := url.Values{}
			if listBranch != "" {
				q.Set("branch", listBranch)
			}
			path := "/repos/" + listRepo + "/baselines"
			if encoded := q.Encode(); encoded != "" {
				path += "?" + encoded
			}
			return runRender(cmd, "GET", path, nil)
		},
	}
	list.Flags().StringVar(&listRepo, "repo", "", "repository ID")
	list.Flags().StringVar(&listBranch, "branch", "", "branch filter")

	var createRepo, createName, createScan, createBranch, createStrategy string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a repository baseline from a scan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if createRepo == "" || createName == "" || createScan == "" {
				return fmt.Errorf("--repo, --name, and --scan are required")
			}
			body := map[string]any{"name": createName, "scan_id": createScan}
			if createBranch != "" {
				body["branch"] = createBranch
			}
			if createStrategy != "" {
				body["strategy"] = createStrategy
			}
			return runRender(cmd, "POST", "/repos/"+createRepo+"/baselines", body)
		},
	}
	create.Flags().StringVar(&createRepo, "repo", "", "repository ID")
	create.Flags().StringVar(&createName, "name", "", "baseline name")
	create.Flags().StringVar(&createScan, "scan", "", "source scan ID")
	create.Flags().StringVar(&createBranch, "branch", "", "baseline branch")
	create.Flags().StringVar(&createStrategy, "strategy", "", "baseline strategy label")

	cmd.AddCommand(list, create)
	return cmd
}

// --- comparisons ------------------------------------------------------------

func newCompareCmd() *cobra.Command {
	var scanID, baselineID string
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare a scan to a baseline scan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if scanID == "" || baselineID == "" {
				return fmt.Errorf("--scan and --baseline are required")
			}
			return runRender(cmd, "POST", "/scans/"+scanID+"/compare", map[string]any{
				"baseline_scan_id": baselineID,
			})
		},
	}
	cmd.Flags().StringVar(&scanID, "scan", "", "current scan ID")
	cmd.Flags().StringVar(&baselineID, "baseline", "", "baseline scan ID")
	return cmd
}

// --- SARIF ------------------------------------------------------------------

func newSarifCmd() *cobra.Command {
	cmd := group("sarif", "Import and export SARIF")

	var importRepo, importFile, importBranch, importSource string
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import SARIF findings as a completed scan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if importRepo == "" || importFile == "" {
				return fmt.Errorf("--repo and --file are required")
			}
			data, err := os.ReadFile(importFile)
			if err != nil {
				return fmt.Errorf("read --file: %w", err)
			}
			body := map[string]any{
				"repo_id": importRepo,
				"sarif":   string(data),
			}
			if importBranch != "" {
				body["branch"] = importBranch
			}
			if importSource != "" {
				body["source"] = importSource
			}
			return runRender(cmd, "POST", "/sarif/import", body)
		},
	}
	importCmd.Flags().StringVar(&importRepo, "repo", "", "repository ID")
	importCmd.Flags().StringVar(&importFile, "file", "", "SARIF file path")
	importCmd.Flags().StringVar(&importBranch, "branch", "", "branch label for the imported scan")
	importCmd.Flags().StringVar(&importSource, "source", "", "import source label")

	exportCmd := &cobra.Command{
		Use:   "export <scan-id>",
		Short: "Export a scan as SARIF",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", "/scans/"+args[0]+"/sarif", nil)
		},
	}

	cmd.AddCommand(importCmd, exportCmd)
	return cmd
}

// --- findings ---------------------------------------------------------------

func newFindingCmd() *cobra.Command {
	cmd := group("finding", "Inspect and triage findings")

	var status string
	setStatus := &cobra.Command{
		Use:   "set-status <id>",
		Short: "Change a finding's status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status == "" {
				return fmt.Errorf("--status is required")
			}
			return runRender(cmd, "PUT", "/findings/"+args[0]+"/status", map[string]any{"status": status})
		},
	}
	setStatus.Flags().StringVar(&status, "status", "", "new status (e.g. open, fixed, false_positive)")

	cmd.AddCommand(
		listCmd("/findings", "List findings"),
		getCmd("/findings", "Get a finding"),
		setStatus,
		&cobra.Command{
			Use: "export", Short: "Export findings", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/findings/export", nil) },
		},
		&cobra.Command{
			Use: "trends", Short: "Finding trends over time", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/findings/trends", nil) },
		},
		&cobra.Command{
			Use: "trends-export", Short: "Export finding trends", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runRender(cmd, "GET", "/findings/trends/export", nil)
			},
		},
	)
	return cmd
}

// --- suppressions -----------------------------------------------------------

func newSuppressCmd() *cobra.Command {
	cmd := group("suppress", "Manage durable finding suppressions")

	var listRepo string
	var includeInactive bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List repository suppressions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if listRepo == "" {
				return fmt.Errorf("--repo is required")
			}
			q := url.Values{}
			q.Set("repo_id", listRepo)
			if includeInactive {
				q.Set("include_inactive", "true")
			}
			return runRender(cmd, "GET", "/suppressions?"+q.Encode(), nil)
		},
	}
	list.Flags().StringVar(&listRepo, "repo", "", "repository ID")
	list.Flags().BoolVar(&includeInactive, "include-inactive", false, "include expired or revoked suppressions")

	buildSuppressionBody := func(findingID, repoID, scopeType, scopeValue, branch, reason, expiresAt string) (map[string]any, error) {
		if reason == "" {
			return nil, fmt.Errorf("--reason is required")
		}
		if findingID == "" && (repoID == "" || scopeType == "" || scopeValue == "") {
			return nil, fmt.Errorf("--repo, --scope-type, and --scope-value are required unless --finding is set")
		}
		body := map[string]any{"reason": reason}
		if findingID != "" {
			body["finding_id"] = findingID
		}
		if repoID != "" {
			body["repo_id"] = repoID
		}
		if scopeType != "" {
			body["scope_type"] = scopeType
		}
		if scopeValue != "" {
			body["scope_value"] = scopeValue
		}
		if branch != "" {
			body["branch"] = branch
		}
		if expiresAt != "" {
			body["expires_at"] = expiresAt
		}
		return body, nil
	}

	var addFinding, addRepo, addScopeType, addScopeValue, addBranch, addReason, addExpires string
	add := &cobra.Command{
		Use:   "add",
		Short: "Create a durable suppression",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := buildSuppressionBody(addFinding, addRepo, addScopeType, addScopeValue, addBranch, addReason, addExpires)
			if err != nil {
				return err
			}
			return runRender(cmd, "POST", "/suppressions", body)
		},
	}
	add.Flags().StringVar(&addFinding, "finding", "", "finding ID to suppress")
	add.Flags().StringVar(&addRepo, "repo", "", "repository ID for scoped suppression")
	add.Flags().StringVar(&addScopeType, "scope-type", "", "scope type (stable_fingerprint, fingerprint, rule, fine_category, path_glob, package_advisory)")
	add.Flags().StringVar(&addScopeValue, "scope-value", "", "scope value")
	add.Flags().StringVar(&addBranch, "branch", "", "optional branch scope")
	add.Flags().StringVar(&addReason, "reason", "", "required suppression reason")
	add.Flags().StringVar(&addExpires, "expires", "", "RFC3339 expiration timestamp")

	var prevFinding, prevRepo, prevScopeType, prevScopeValue, prevBranch, prevReason, prevExpires string
	preview := &cobra.Command{
		Use:   "preview",
		Short: "Preview suppression impact",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := buildSuppressionBody(prevFinding, prevRepo, prevScopeType, prevScopeValue, prevBranch, prevReason, prevExpires)
			if err != nil {
				return err
			}
			return runRender(cmd, "POST", "/suppressions/preview", body)
		},
	}
	preview.Flags().StringVar(&prevFinding, "finding", "", "finding ID to suppress")
	preview.Flags().StringVar(&prevRepo, "repo", "", "repository ID for scoped suppression")
	preview.Flags().StringVar(&prevScopeType, "scope-type", "", "scope type")
	preview.Flags().StringVar(&prevScopeValue, "scope-value", "", "scope value")
	preview.Flags().StringVar(&prevBranch, "branch", "", "optional branch scope")
	preview.Flags().StringVar(&prevReason, "reason", "", "required suppression reason")
	preview.Flags().StringVar(&prevExpires, "expires", "", "RFC3339 expiration timestamp")

	cmd.AddCommand(
		list,
		add,
		preview,
		deleteCmd("revoke <id>", "Revoke a suppression", "/suppressions/%s"),
	)
	return cmd
}

// --- policies ---------------------------------------------------------------

func newPolicyCmd() *cobra.Command {
	cmd := group("policy", "Manage quality gate policies")

	var listScope, listScopeID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List quality policies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if listScope != "" {
				q.Set("scope", listScope)
			}
			if listScopeID != "" {
				q.Set("scope_id", listScopeID)
			}
			path := "/policies"
			if encoded := q.Encode(); encoded != "" {
				path += "?" + encoded
			}
			return runRender(cmd, "GET", path, nil)
		},
	}
	list.Flags().StringVar(&listScope, "scope", "", "policy scope filter")
	list.Flags().StringVar(&listScopeID, "scope-id", "", "policy scope ID filter")

	addPolicyFlags := func(c *cobra.Command, name, scope, scopeID, mode, rulesJSON, rulesFile *string, enabled *bool) {
		c.Flags().StringVar(name, "name", "", "policy name")
		c.Flags().StringVar(scope, "scope", "", "policy scope")
		c.Flags().StringVar(scopeID, "scope-id", "", "policy scope ID")
		c.Flags().StringVar(mode, "mode", "", "policy mode")
		c.Flags().StringVar(rulesJSON, "rules-json", "", "policy rules JSON")
		c.Flags().StringVar(rulesFile, "rules-file", "", "file containing policy rules JSON")
		c.Flags().BoolVar(enabled, "enabled", true, "policy enabled")
	}
	buildPolicyBody := func(cmd *cobra.Command, name, scope, scopeID, mode, rulesJSON, rulesFile string, enabled bool) (map[string]any, error) {
		if name == "" {
			return nil, fmt.Errorf("--name is required")
		}
		if rulesFile != "" {
			data, err := os.ReadFile(rulesFile)
			if err != nil {
				return nil, fmt.Errorf("read --rules-file: %w", err)
			}
			rulesJSON = string(data)
		}
		body := map[string]any{"name": name}
		if scope != "" {
			body["scope"] = scope
		}
		if scopeID != "" {
			body["scope_id"] = scopeID
		}
		if mode != "" {
			body["mode"] = mode
		}
		if rulesJSON != "" {
			body["rules_json"] = rulesJSON
		}
		if cmd.Flags().Changed("enabled") {
			body["enabled"] = enabled
		}
		return body, nil
	}

	var createName, createScope, createScopeID, createMode, createRulesJSON, createRulesFile string
	var createEnabled bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create or replace a quality policy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := buildPolicyBody(cmd, createName, createScope, createScopeID, createMode, createRulesJSON, createRulesFile, createEnabled)
			if err != nil {
				return err
			}
			return runRender(cmd, "POST", "/policies", body)
		},
	}
	addPolicyFlags(create, &createName, &createScope, &createScopeID, &createMode, &createRulesJSON, &createRulesFile, &createEnabled)

	var updateName, updateScope, updateScopeID, updateMode, updateRulesJSON, updateRulesFile string
	var updateEnabled bool
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a quality policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildPolicyBody(cmd, updateName, updateScope, updateScopeID, updateMode, updateRulesJSON, updateRulesFile, updateEnabled)
			if err != nil {
				return err
			}
			return runRender(cmd, "PUT", "/policies/"+args[0], body)
		},
	}
	addPolicyFlags(update, &updateName, &updateScope, &updateScopeID, &updateMode, &updateRulesJSON, &updateRulesFile, &updateEnabled)

	cmd.AddCommand(list, create, update)
	return cmd
}

// --- fixes ------------------------------------------------------------------

func newFixCmd() *cobra.Command {
	cmd := group("fix", "Manage AI fix runs")

	var scanID string
	var findingIDs, severity []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Start a fix run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if scanID == "" {
				return fmt.Errorf("--scan is required")
			}
			body := map[string]any{"scan_id": scanID}
			if len(findingIDs) > 0 {
				body["finding_ids"] = findingIDs
			}
			if len(severity) > 0 {
				body["severity"] = severity
			}
			return runRender(cmd, "POST", "/fixes", body)
		},
	}
	create.Flags().StringVar(&scanID, "scan", "", "scan ID to fix findings from")
	create.Flags().StringArrayVar(&findingIDs, "finding", nil, "specific finding ID (repeatable)")
	create.Flags().StringArrayVar(&severity, "severity", nil, "severity filter (repeatable)")

	cmd.AddCommand(
		listCmd("/fixes", "List fixes"),
		getCmd("/fixes", "Get a fix"),
		create,
		watchCmd("Stream a fix's progress", "/fixes/%s/stream"),
		deleteCmd("cancel <id>", "Cancel a fix", "/fixes/%s"),
	)
	return cmd
}

// --- loops ------------------------------------------------------------------

func newLoopCmd() *cobra.Command {
	cmd := group("loop", "Manage scan/fix loops")

	var repoID, severity, strategy, engine string
	var maxIter int
	create := &cobra.Command{
		Use:   "create",
		Short: "Start a loop",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repoID == "" {
				return fmt.Errorf("--repo is required")
			}
			body := map[string]any{"repo_id": repoID}
			if cmd.Flags().Changed("max-iterations") {
				body["max_iterations"] = maxIter
			}
			if severity != "" {
				body["severity_filter"] = severity
			}
			if strategy != "" {
				body["rescan_strategy"] = strategy
			}
			if engine != "" {
				body["engine"] = engine
			}
			return runRender(cmd, "POST", "/loops", body)
		},
	}
	create.Flags().StringVar(&repoID, "repo", "", "repository ID")
	create.Flags().IntVar(&maxIter, "max-iterations", 0, "maximum loop iterations")
	create.Flags().StringVar(&severity, "severity", "", "severity filter")
	create.Flags().StringVar(&strategy, "strategy", "", "rescan strategy")
	create.Flags().StringVar(&engine, "engine", "", "AI engine")

	cmd.AddCommand(
		listCmd("/loops", "List loops"),
		getCmd("/loops", "Get a loop"),
		create,
		watchCmd("Stream a loop's progress", "/loops/%s/stream"),
		actionCmd("pause <id>", "Pause a loop", "PUT", "/loops/%s/pause"),
		actionCmd("resume <id>", "Resume a loop", "PUT", "/loops/%s/resume"),
		deleteCmd("stop <id>", "Stop a loop", "/loops/%s"),
	)
	return cmd
}

// --- users ------------------------------------------------------------------

func newUserCmd() *cobra.Command {
	cmd := group("user", "Manage users (admin)")

	var email, password string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if email == "" || password == "" {
				return fmt.Errorf("--email and --password are required")
			}
			return runRender(cmd, "POST", "/users", map[string]any{"email": email, "password": password})
		},
	}
	create.Flags().StringVar(&email, "email", "", "user email")
	create.Flags().StringVar(&password, "password", "", "initial password")

	cmd.AddCommand(
		listCmd("/users", "List users"),
		create,
		deleteCmd("delete <id>", "Delete a user", "/users/%s"),
	)
	return cmd
}

// --- settings ---------------------------------------------------------------

func newSettingsCmd() *cobra.Command {
	cmd := group("settings", "Read and update server settings")

	var kv map[string]string
	set := &cobra.Command{
		Use:   "set",
		Short: "Update settings (--set key=value, repeatable)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(kv) == 0 {
				return fmt.Errorf("at least one --set key=value is required")
			}
			return runRender(cmd, "PUT", "/settings", kv)
		},
	}
	set.Flags().StringToStringVar(&kv, "set", nil, "a key=value setting (repeatable)")

	cmd.AddCommand(
		&cobra.Command{
			Use: "get", Short: "Get all settings", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/settings", nil) },
		},
		set,
	)
	return cmd
}

// --- AI prompts -------------------------------------------------------------

func newPromptCmd() *cobra.Command {
	cmd := group("prompt", "Manage AI prompt templates")

	var scope, scopeID, promptType, section, content string
	set := &cobra.Command{
		Use:   "set",
		Short: "Create or update a prompt template",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if promptType == "" || section == "" {
				return fmt.Errorf("--type and --section are required")
			}
			body := map[string]any{
				"scope": scope, "scope_id": scopeID,
				"prompt_type": promptType, "section": section, "content": content,
			}
			return runRender(cmd, "PUT", "/ai-prompts", body)
		},
	}
	set.Flags().StringVar(&scope, "scope", "global", "template scope")
	set.Flags().StringVar(&scopeID, "scope-id", "", "scope ID")
	set.Flags().StringVar(&promptType, "type", "", "prompt type")
	set.Flags().StringVar(&section, "section", "", "prompt section")
	set.Flags().StringVar(&content, "content", "", "template content")

	var prevType, prevCollection string
	preview := &cobra.Command{
		Use:   "preview",
		Short: "Preview a rendered prompt",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prevType == "" {
				return fmt.Errorf("--type is required")
			}
			return runRender(cmd, "POST", "/ai-prompts/preview", map[string]any{
				"prompt_type": prevType, "collection_id": prevCollection,
			})
		},
	}
	preview.Flags().StringVar(&prevType, "type", "", "prompt type")
	preview.Flags().StringVar(&prevCollection, "collection", "", "collection ID for scope resolution")

	cmd.AddCommand(
		listCmd("/ai-prompts", "List prompt templates"),
		&cobra.Command{
			Use: "defaults", Short: "Show default prompts", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/ai-prompts/defaults", nil) },
		},
		set,
		preview,
		deleteCmd("delete <id>", "Delete a prompt template", "/ai-prompts/%s"),
	)
	return cmd
}

// --- AI providers -----------------------------------------------------------

func newProviderCmd() *cobra.Command {
	cmd := group("provider", "Inspect configured AI providers")
	cmd.AddCommand(listCmd("/ai-providers", "List AI providers"))
	return cmd
}

// --- secrets ----------------------------------------------------------------

func newSecretCmd() *cobra.Command {
	cmd := group("secret", "Manage stored secrets")

	var keyType, keyName, value string
	create := &cobra.Command{
		Use:   "create",
		Short: "Store a secret",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keyName == "" || value == "" {
				return fmt.Errorf("--name and --value are required")
			}
			body := map[string]any{"key_name": keyName, "value": value}
			if keyType != "" {
				body["key_type"] = keyType
			}
			return runRender(cmd, "POST", "/config/secrets", body)
		},
	}
	create.Flags().StringVar(&keyType, "type", "", "secret type")
	create.Flags().StringVar(&keyName, "name", "", "secret name")
	create.Flags().StringVar(&value, "value", "", "secret value")

	cmd.AddCommand(
		listCmd("/config/secrets", "List secrets"),
		create,
		deleteCmd("delete <id>", "Delete a secret", "/config/secrets/%s"),
	)
	return cmd
}

// --- plugins ----------------------------------------------------------------

func newPluginCmd() *cobra.Command {
	cmd := group("plugin", "Manage scanner plugins")
	cmd.AddCommand(
		listCmd("/config/plugins", "List plugins"),
		&cobra.Command{
			Use: "install <name>", Short: "Install a plugin", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, "POST", "/config/plugins/"+args[0]+"/install", nil)
			},
		},
	)
	return cmd
}

// --- scanners ---------------------------------------------------------------

func newScannerCmd() *cobra.Command {
	cmd := group("scanner", "Manage the container scanner backend")
	var planRepo string
	var planLanguages []string
	var planTools []string
	var planDisabledTools []string
	var planCheckAvailability bool
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Explain which scanners would run or skip",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{}
			if planRepo != "" {
				body["repo_id"] = planRepo
			}
			if len(planLanguages) > 0 {
				body["languages"] = planLanguages
			}
			if len(planTools) > 0 {
				body["tools"] = planTools
			}
			if len(planDisabledTools) > 0 {
				body["disabled_tools"] = planDisabledTools
			}
			if planCheckAvailability {
				body["check_availability"] = true
			}
			return runRender(cmd, "POST", "/scanners/plan", body)
		},
	}
	planCmd.Flags().StringVar(&planRepo, "repo", "", "repository ID for cached/local language detection")
	planCmd.Flags().StringSliceVar(&planLanguages, "language", nil, "detected language override")
	planCmd.Flags().StringSliceVar(&planTools, "tools", nil, "explicit tool list")
	planCmd.Flags().StringSliceVar(&planDisabledTools, "disabled-tools", nil, "tools to disable")
	planCmd.Flags().BoolVar(&planCheckAvailability, "check-availability", false, "check local scanner availability")
	cmd.AddCommand(
		listCmd("/scanners/tools", "List scanner tools"),
		listCmd("/scanners/images", "List scanner images"),
		listCmd("/scanners/list", "List scanners"),
		planCmd,
		&cobra.Command{
			Use: "tool <name>", Short: "Show scanner tool metadata", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, "GET", "/scanners/tools/"+args[0], nil)
			},
		},
		&cobra.Command{
			Use: "check-updates", Short: "Refresh scanner tool version checks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runRender(cmd, "POST", "/scanners/tools/check-updates", nil)
			},
		},
		&cobra.Command{
			Use: "check-update <name>", Short: "Refresh one scanner tool version check", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, "POST", "/scanners/tools/"+args[0]+"/check-update", nil)
			},
		},
		&cobra.Command{
			Use: "plan-upgrades", Short: "Refresh and list scanner tool upgrade status", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, err := resolveClient(cmd)
				if err != nil {
					return err
				}
				if _, err := c.Do(cmd.Context(), "POST", "/scanners/tools/check-updates", map[string]any{"force": true}); err != nil {
					return err
				}
				env, err := c.Do(cmd.Context(), "GET", "/scanners/tools", nil)
				if err != nil {
					return err
				}
				return Render(cmd.OutOrStdout(), resolveOutput(cmd), env)
			},
		},
		&cobra.Command{
			Use: "config", Short: "Show scanner config", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/scanners/config", nil) },
		},
		&cobra.Command{
			Use: "doctor", Short: "Run scanner diagnostics", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "POST", "/scanners/doctor", nil) },
		},
		&cobra.Command{
			Use: "pull-all", Short: "Pull every scanner image", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "POST", "/scanners/pull", nil) },
		},
		&cobra.Command{
			Use: "pull-image <name>", Short: "Pull one scanner image", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, "POST", "/scanners/images/pull", map[string]any{"image": args[0]})
			},
		},
	)
	return cmd
}

// --- audit log --------------------------------------------------------------

func newAuditCmd() *cobra.Command {
	cmd := group("audit", "Inspect the request audit log (admin)")
	cmd.AddCommand(listCmd("/audit-log", "List audit-log entries"))
	return cmd
}

// --- system -----------------------------------------------------------------

func newSystemCmd() *cobra.Command {
	cmd := group("system", "Server health and local-path inspection")

	pathQuery := func(endpoint string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			q.Set("path", args[0])
			return runRender(cmd, "GET", endpoint+"?"+q.Encode(), nil)
		}
	}

	cmd.AddCommand(
		&cobra.Command{Use: "health", Short: "Liveness probe", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/health", nil) }},
		&cobra.Command{Use: "ready", Short: "Readiness probe", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/ready", nil) }},
		&cobra.Command{Use: "version", Short: "Server build version", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/version", nil) }},
		&cobra.Command{Use: "setup-status", Short: "First-run setup status", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runRender(cmd, "GET", "/config/setup", nil) }},
		&cobra.Command{Use: "browse <path>", Short: "Browse a local filesystem path", Args: cobra.ExactArgs(1),
			RunE: pathQuery("/browse")},
		&cobra.Command{Use: "git-info <path>", Short: "Inspect a local git repository", Args: cobra.ExactArgs(1),
			RunE: pathQuery("/git-info")},
	)
	return cmd
}
