package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const scannerSupplyChainPath = "/scanner-supply-chain"

func addScannerSupplyChainCommands(parent *cobra.Command) {
	parent.AddCommand(
		&cobra.Command{
			Use: "overview", Short: "Show scanner release freshness and health",
			Annotations: apiAnno("GET", scannerSupplyChainPath+"/overview"), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runRender(cmd, http.MethodGet, scannerSupplyChainPath+"/overview", nil)
			},
		},
		newScannerUpdateCommands(),
		newScannerCandidateCommands(),
		newScannerReleaseCommands(),
		newScannerRolloutCommands(),
		newScannerPolicyCommands(),
		newScannerRegistryCommands(),
		newScannerSignerCommands(),
		newScannerNotificationCommands(),
		newScannerAlertCommands(),
		newScannerAuditCommands(),
	)
}

func newScannerSignerCommands() *cobra.Command {
	group := group("signer", "Configure customer KMS, HSM, keyless, and offline signers")
	group.AddCommand(
		listQueryCommand(
			"list", "List masked signer profiles",
			scannerSupplyChainPath+"/signers",
			apiAnno("GET", scannerSupplyChainPath+"/signers"),
		),
		scannerGetByIDCommand(
			"show <id>", "Show one masked signer profile",
			scannerSupplyChainPath+"/signers/",
			apiAnno("GET", scannerSupplyChainPath+"/signers/{}"),
		),
	)
	create := &cobra.Command{
		Use: "create <file>", Short: "Create a signer profile from JSON or YAML",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/signers"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readObjectFile(args[0])
			if err != nil {
				return err
			}
			return runScannerCommand(
				cmd, http.MethodPost, scannerSupplyChainPath+"/signers",
				body, "-", "", false,
			)
		},
	}
	var rotateVersion string
	rotate := &cobra.Command{
		Use: "rotate <id> <file>", Short: "Atomically activate a replacement signer revision",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/signers/{}/rotate"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rotateVersion == "" {
				return errors.New("--if-match is required")
			}
			body, err := readObjectFile(args[1])
			if err != nil {
				return err
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/signers/"+url.PathEscape(args[0])+"/rotate",
				body, "-", rotateVersion, false,
			)
		},
	}
	rotate.Flags().StringVar(&rotateVersion, "if-match", "", "current signer revision")
	var revokeVersion, revokeReason string
	revoke := &cobra.Command{
		Use: "revoke <id>", Short: "Revoke a signer revision immediately",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/signers/{}/revoke"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if revokeVersion == "" || strings.TrimSpace(revokeReason) == "" {
				return errors.New("--if-match and --reason are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/signers/"+url.PathEscape(args[0])+"/revoke",
				map[string]any{"reason": revokeReason}, "-", revokeVersion, false,
			)
		},
	}
	revoke.Flags().StringVar(&revokeVersion, "if-match", "", "current signer revision")
	revoke.Flags().StringVar(&revokeReason, "reason", "", "auditable revocation reason")
	group.AddCommand(create, rotate, revoke)
	return group
}

func newScannerUpdateCommands() *cobra.Command {
	group := group("update", "Discover and inspect scanner dependency updates")
	var (
		tools          []string
		components     []string
		reason         string
		idempotencyKey string
		watch          bool
	)
	check := &cobra.Command{
		Use: "check", Short: "Enqueue a complete or selected update discovery",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/discovery-runs"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return errors.New("--reason is required")
			}
			scope := map[string]any{"type": "all"}
			if len(tools) != 0 || len(components) != 0 {
				scope = map[string]any{"type": "selected", "tools": tools, "components": components}
			}
			return runScannerCommand(cmd, http.MethodPost, scannerSupplyChainPath+"/discovery-runs",
				map[string]any{"scope": scope, "reason": reason}, idempotencyKey, "", watch)
		},
	}
	check.Flags().StringSliceVar(&tools, "tool", nil, "tool to check (repeatable)")
	check.Flags().StringSliceVar(&components, "component", nil, "exact component kind/name to check (repeatable)")
	check.Flags().StringVar(&reason, "reason", "", "operator reason")
	check.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "stable command key (default: generated UUID)")
	check.Flags().BoolVar(&watch, "watch", false, "watch durable events until terminal")

	history := listQueryCommand(
		"history", "List update-discovery history",
		scannerSupplyChainPath+"/discovery-runs", apiAnno("GET", scannerSupplyChainPath+"/discovery-runs"),
	)
	updates := listQueryCommand(
		"list", "List update items from the latest discovery",
		scannerSupplyChainPath+"/updates", apiAnno("GET", scannerSupplyChainPath+"/updates"),
	)
	show := scannerGetByIDCommand(
		"show <id>", "Show one update-discovery run",
		scannerSupplyChainPath+"/discovery-runs/", apiAnno("GET", scannerSupplyChainPath+"/discovery-runs/{}"),
	)
	events := scannerEventsCommand(
		"events <id>", "Stream durable update-discovery events",
		scannerSupplyChainPath+"/discovery-runs/", apiAnno("GET", scannerSupplyChainPath+"/discovery-runs/{}/events"),
	)
	var cancelKey, cancelVersion string
	cancel := &cobra.Command{
		Use: "cancel <id>", Short: "Cancel an update-discovery run",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/discovery-runs/{}/cancel"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cancelVersion == "" {
				return errors.New("--if-match is required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/discovery-runs/"+url.PathEscape(args[0])+"/cancel",
				nil, cancelKey, cancelVersion, false)
		},
	}
	cancel.Flags().StringVar(&cancelKey, "idempotency-key", "", "stable command key")
	cancel.Flags().StringVar(&cancelVersion, "if-match", "", "current resource version")
	group.AddCommand(check, updates, history, show, events, cancel)
	return group
}

func newScannerCandidateCommands() *cobra.Command {
	group := group("candidate", "Build, approve, and publish scanner release candidates")
	group.AddCommand(
		listQueryCommand("list", "List scanner release candidates", scannerSupplyChainPath+"/candidates", apiAnno("GET", scannerSupplyChainPath+"/candidates")),
		scannerGetByIDCommand("show <id>", "Show candidate gates and evidence", scannerSupplyChainPath+"/candidates/", apiAnno("GET", scannerSupplyChainPath+"/candidates/{}")),
		scannerArtifactDiffCommand("candidate", scannerSupplyChainPath+"/candidates/"),
		scannerEventsCommand("events <id>", "Stream durable candidate events", scannerSupplyChainPath+"/candidates/", apiAnno("GET", scannerSupplyChainPath+"/candidates/{}/events")),
	)

	var fromRun, definition, lockDigest, lockURI, proposalURL, reason, key string
	var selected []string
	var watch bool
	create := &cobra.Command{
		Use: "create", Short: "Create a scanner release candidate",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return errors.New("candidate reason is required")
			}
			body := map[string]any{
				"discovery_run_id": fromRun, "definition_commit": definition,
				"lock_digest": lockDigest, "lock_uri": lockURI, "proposal_url": proposalURL,
				"selected_items": selected, "reason": strings.TrimSpace(reason),
			}
			return runScannerCommand(cmd, http.MethodPost, scannerSupplyChainPath+"/candidates", body, key, "", watch)
		},
	}
	create.Flags().StringVar(&fromRun, "from-run", "", "completed discovery run ID")
	create.Flags().StringVar(&definition, "definition-commit", "", "definition commit (default: server lock identity)")
	create.Flags().StringVar(&lockDigest, "lock-digest", "", "exact generated lock digest")
	create.Flags().StringVar(&lockURI, "lock-uri", "", "immutable lock artifact URI")
	create.Flags().StringVar(&proposalURL, "proposal-url", "", "definition proposal URL")
	create.Flags().StringSliceVar(&selected, "item", nil, "selected discovery item ID (repeatable)")
	create.Flags().StringVar(&reason, "reason", "", "auditable candidate reason")
	create.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	create.Flags().BoolVar(&watch, "watch", false, "watch durable events")
	group.AddCommand(create)

	var retryKey, retryVersion string
	retry := &cobra.Command{
		Use: "retry <id>", Short: "Retry a safely resumable blocked candidate",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates/{}/retry"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if retryVersion == "" {
				return errors.New("--if-match is required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/candidates/"+url.PathEscape(args[0])+"/retry",
				nil, retryKey, retryVersion, false)
		},
	}
	retry.Flags().StringVar(&retryKey, "idempotency-key", "", "stable command key")
	retry.Flags().StringVar(&retryVersion, "if-match", "", "current candidate version")
	var cancelKey, cancelVersion, cancelReason string
	cancel := &cobra.Command{
		Use: "cancel <id>", Short: "Cooperatively cancel candidate build work",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates/{}/cancel"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cancelVersion == "" || strings.TrimSpace(cancelReason) == "" {
				return errors.New("--if-match and --reason are required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/candidates/"+url.PathEscape(args[0])+"/cancel",
				map[string]any{"reason": cancelReason}, cancelKey, cancelVersion, false)
		},
	}
	cancel.Flags().StringVar(&cancelKey, "idempotency-key", "", "stable command key")
	cancel.Flags().StringVar(&cancelVersion, "if-match", "", "current candidate version")
	cancel.Flags().StringVar(&cancelReason, "reason", "", "operator reason")
	group.AddCommand(retry, cancel)

	group.AddCommand(scannerCandidateDecisionCommand("approve"), scannerCandidateDecisionCommand("reject"))

	var exceptionGate, exceptionOwner, exceptionReason, exceptionControl string
	var exceptionEvidence, exceptionExpires, exceptionKey string
	exception := &cobra.Command{
		Use: "exception <id>", Short: "Record a scoped, approved, expiring candidate exception",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates/{}/exceptions"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if exceptionGate == "" || exceptionOwner == "" ||
				strings.TrimSpace(exceptionReason) == "" ||
				strings.TrimSpace(exceptionControl) == "" || exceptionEvidence == "" ||
				exceptionExpires == "" {
				return errors.New("--gate, --owner, --reason, --compensating-control, --evidence-digest, and --expires-at are required")
			}
			expires, err := time.Parse(time.RFC3339, exceptionExpires)
			if err != nil || !expires.After(time.Now()) {
				return errors.New("--expires-at must be a future RFC3339 timestamp")
			}
			body := map[string]any{
				"gate": exceptionGate, "owner_id": exceptionOwner,
				"reason":               strings.TrimSpace(exceptionReason),
				"compensating_control": strings.TrimSpace(exceptionControl),
				"evidence_digest":      exceptionEvidence,
				"expires_at":           expires.UTC().Format(time.RFC3339),
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/candidates/"+url.PathEscape(args[0])+"/exceptions",
				body, exceptionKey, "", false)
		},
	}
	exception.Flags().StringVar(&exceptionGate, "gate", "", "exception-eligible policy gate")
	exception.Flags().StringVar(&exceptionOwner, "owner", "", "accountable exception owner identity")
	exception.Flags().StringVar(&exceptionReason, "reason", "", "exception justification")
	exception.Flags().StringVar(&exceptionControl, "compensating-control", "", "active compensating control")
	exception.Flags().StringVar(&exceptionEvidence, "evidence-digest", "", "exact failing gate evidence digest")
	exception.Flags().StringVar(&exceptionExpires, "expires-at", "", "future RFC3339 expiration")
	exception.Flags().StringVar(&exceptionKey, "idempotency-key", "", "stable command key")
	group.AddCommand(exception)

	var publicationName, publicationReceipt, publicationReason, publicationKey string
	var publicationWatch bool
	publish := &cobra.Command{
		Use: "publish <id>", Short: "Record a verified immutable release publication",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates/{}/publish"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if publicationReceipt == "" || strings.TrimSpace(publicationReason) == "" {
				return errors.New("--receipt-digest and --reason are required")
			}
			body := map[string]any{
				"name": publicationName, "receipt_digest": publicationReceipt,
				"reason": publicationReason,
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/candidates/"+url.PathEscape(args[0])+"/publish",
				body, publicationKey, "", publicationWatch)
		},
	}
	publish.Flags().StringVar(&publicationName, "name", "", "optional scanner-set-YYYY.WW.N release name")
	publish.Flags().StringVar(&publicationReceipt, "receipt-digest", "", "server-verified completed build receipt digest")
	publish.Flags().StringVar(&publicationReason, "reason", "", "operator publication reason")
	publish.Flags().StringVar(&publicationKey, "idempotency-key", "", "stable command key")
	publish.Flags().BoolVar(&publicationWatch, "watch", false, "watch durable events")
	group.AddCommand(publish)
	return group
}

func scannerCandidateDecisionCommand(action string) *cobra.Command {
	var lockDigest, decisionDigest, evidenceDigest, reason, key string
	command := &cobra.Command{
		Use: action + " <id>", Short: strings.Title(action) + " an exact candidate evidence set", //nolint:staticcheck
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/candidates/{}/"+action), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				return errors.New("--reason is required")
			}
			body := map[string]any{
				"decision": action, "reason": reason, "lock_digest": lockDigest,
				"policy_decision_digest": decisionDigest, "evidence_digest": evidenceDigest,
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/candidates/"+url.PathEscape(args[0])+"/"+action,
				body, key, "", false)
		},
	}
	command.Flags().StringVar(&lockDigest, "lock-digest", "", "candidate lock digest")
	command.Flags().StringVar(&decisionDigest, "policy-decision-digest", "", "bound policy decision digest")
	command.Flags().StringVar(&evidenceDigest, "evidence-digest", "", "aggregate evidence digest")
	command.Flags().StringVar(&reason, "reason", "", "approval/rejection reason")
	command.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	return command
}

func newScannerReleaseCommands() *cobra.Command {
	group := group("release", "Inspect and promote immutable scanner releases")
	group.AddCommand(
		listQueryCommand("list", "List scanner releases", scannerSupplyChainPath+"/releases", apiAnno("GET", scannerSupplyChainPath+"/releases")),
		scannerGetByIDCommand("show <id>", "Show release inventory", scannerSupplyChainPath+"/releases/", apiAnno("GET", scannerSupplyChainPath+"/releases/{}")),
		scannerArtifactDiffCommand("release", scannerSupplyChainPath+"/releases/"),
		scannerEventsCommand("events <id>", "Stream durable release events", scannerSupplyChainPath+"/releases/", apiAnno("GET", scannerSupplyChainPath+"/releases/{}/events")),
		newScannerReleaseExportCommand(),
		newScannerReleaseImportCommand(),
		newScannerLegacyReleaseImportCommand(),
	)
	group.AddCommand(
		&cobra.Command{
			Use: "compare <from-id> <to-id>", Short: "Compare two immutable release inventories",
			Annotations: apiAnno("GET", scannerSupplyChainPath+"/releases/compare"), Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				values := url.Values{"from": {args[0]}, "to": {args[1]}}
				return runRender(cmd, http.MethodGet, scannerSupplyChainPath+"/releases/compare?"+values.Encode(), nil)
			},
		},
		&cobra.Command{
			Use: "verify <id>", Short: "Verify immutable release evidence",
			Annotations: apiAnno("POST", scannerSupplyChainPath+"/releases/{}/verify"), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runRender(cmd, http.MethodPost,
					scannerSupplyChainPath+"/releases/"+url.PathEscape(args[0])+"/verify", map[string]any{})
			},
		},
	)
	var target, strategy, reason, key string
	var watch bool
	promote := &cobra.Command{
		Use: "promote <id>", Short: "Start a canary-first scanner release rollout",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/releases/{}/promote"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if target == "" || reason == "" {
				return errors.New("--target and --reason are required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/releases/"+url.PathEscape(args[0])+"/promote",
				map[string]any{"target": target, "strategy": strategy, "reason": reason}, key, "", watch)
		},
	}
	promote.Flags().StringVar(&target, "target", "", "deployment target")
	promote.Flags().StringVar(&strategy, "strategy", "canary_then_stable", "rollout strategy")
	promote.Flags().StringVar(&reason, "reason", "", "operator reason")
	promote.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	promote.Flags().BoolVar(&watch, "watch", false, "watch rollout events")
	group.AddCommand(promote)
	group.AddCommand(scannerReleaseStateCommand("deprecate"), scannerReleaseStateCommand("revoke"))
	return group
}

func newScannerLegacyReleaseImportCommand() *cobra.Command {
	var reason, key string
	var digests map[string]string
	command := &cobra.Command{
		Use:         "import-legacy-config",
		Short:       "Snapshot configured legacy scanner references without changing runtime assignments",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/legacy-release-imports"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return errors.New("--reason is required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/legacy-release-imports",
				map[string]any{"reason": reason, "resolved_digests": digests},
				key, "", false)
		},
	}
	command.Flags().StringVar(&reason, "reason", "", "auditable import reason")
	command.Flags().StringToStringVar(&digests, "digest", nil, "configured image key to immutable digest (repeatable, key=sha256:...)")
	command.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	return command
}

func scannerArtifactDiffCommand(ownerType, pathPrefix string) *cobra.Command {
	return &cobra.Command{
		Use:   "diff <id> <manifest|lock>",
		Short: "Show a bounded, verified scanner " + ownerType + " artifact diff",
		Annotations: apiAnno(
			http.MethodGet,
			pathPrefix+"{}/diffs/{}",
		),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := strings.ToLower(strings.TrimSpace(args[1]))
			if kind != "manifest" && kind != "lock" {
				return errors.New("diff kind must be manifest or lock")
			}
			path := pathPrefix + url.PathEscape(args[0]) + "/diffs/" + kind
			return runRender(cmd, http.MethodGet, path, nil)
		},
	}
}

func scannerReleaseStateCommand(action string) *cobra.Command {
	var reason, impact, key, version string
	command := &cobra.Command{
		Use: action + " <id>", Short: strings.Title(action) + " a scanner release", //nolint:staticcheck
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/releases/{}/"+action), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" || version == "" {
				return errors.New("--reason and --if-match are required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/releases/"+url.PathEscape(args[0])+"/"+action,
				map[string]any{"reason": reason, "impact_policy": impact}, key, version, false)
		},
	}
	command.Flags().StringVar(&reason, "reason", "", "operator reason")
	command.Flags().StringVar(&impact, "impact-policy", "", "queued/active scan impact policy")
	command.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	command.Flags().StringVar(&version, "if-match", "", "current release version")
	return command
}

func newScannerRolloutCommands() *cobra.Command {
	group := group("rollout", "Monitor and control scanner release rollouts")
	group.AddCommand(
		listQueryCommand("list", "List scanner rollouts", scannerSupplyChainPath+"/rollouts", apiAnno("GET", scannerSupplyChainPath+"/rollouts")),
		scannerGetByIDCommand("show <id>", "Show rollout cohorts and health", scannerSupplyChainPath+"/rollouts/", apiAnno("GET", scannerSupplyChainPath+"/rollouts/{}")),
		scannerEventsCommand("events <id>", "Stream durable rollout events", scannerSupplyChainPath+"/rollouts/", apiAnno("GET", scannerSupplyChainPath+"/rollouts/{}/events")),
		scannerRolloutActionCommand("pause"),
		scannerRolloutActionCommand("resume"),
		scannerRolloutActionCommand("rollback"),
	)
	return group
}

func scannerRolloutActionCommand(action string) *cobra.Command {
	var reason, key, version string
	var watch bool
	command := &cobra.Command{
		Use: action + " <id>", Short: strings.Title(action) + " a scanner rollout", //nolint:staticcheck
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/rollouts/{}/"+action), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" || version == "" {
				return errors.New("--reason and --if-match are required")
			}
			return runScannerCommand(cmd, http.MethodPost,
				scannerSupplyChainPath+"/rollouts/"+url.PathEscape(args[0])+"/"+action,
				map[string]any{"reason": reason}, key, version, watch)
		},
	}
	command.Flags().StringVar(&reason, "reason", "", "operator reason")
	command.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	command.Flags().StringVar(&version, "if-match", "", "current rollout version")
	command.Flags().BoolVar(&watch, "watch", false, "watch rollout events")
	return command
}

func newScannerPolicyCommands() *cobra.Command {
	group := group("policy", "Validate and manage scanner release policy")
	group.AddCommand(&cobra.Command{
		Use: "get", Short: "Get active scanner release policy",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/policy"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd, http.MethodGet, scannerSupplyChainPath+"/policy", nil)
		},
	})
	var validateFile string
	validate := &cobra.Command{
		Use: "validate <file>", Short: "Validate scanner release policy without saving it",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/policy/validate"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			object, err := readObjectFile(args[0])
			if err != nil {
				return err
			}
			if _, exists := object["rules"]; !exists {
				return errors.New("policy file must contain schedule and rules objects")
			}
			if _, exists := object["schedule"]; !exists {
				return errors.New("policy file must contain schedule and rules objects")
			}
			return runRender(cmd, http.MethodPost, scannerSupplyChainPath+"/policy/validate", object)
		},
	}
	validate.Flags().StringVar(&validateFile, "format", "", "reserved for compatibility")
	group.AddCommand(validate)
	var file, version string
	apply := &cobra.Command{
		Use: "apply <file>", Short: "Apply a validated scanner release policy revision",
		Annotations: apiAnno("PUT", scannerSupplyChainPath+"/policy"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version == "" {
				return errors.New("--if-match is required")
			}
			body, err := readObjectFile(args[0])
			if err != nil {
				return err
			}
			return runScannerCommand(cmd, http.MethodPut, scannerSupplyChainPath+"/policy", body, "", version, false)
		},
	}
	apply.Flags().StringVar(&file, "file", "", "deprecated; use positional file")
	apply.Flags().StringVar(&version, "if-match", "", "active policy revision")
	history := &cobra.Command{
		Use: "history", Short: "List immutable scanner policy revisions",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/policy/revisions"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd, http.MethodGet, scannerSupplyChainPath+"/policy/revisions", nil)
		},
	}
	var dryRunFile string
	dryRun := &cobra.Command{
		Use:         "dry-run <candidate-id> <file>",
		Short:       "Evaluate a proposed policy against a historical candidate",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/policy/dry-run"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readObjectFile(args[1])
			if err != nil {
				return err
			}
			body["candidate_id"] = args[0]
			return runRender(cmd, http.MethodPost, scannerSupplyChainPath+"/policy/dry-run", body)
		},
	}
	dryRun.Flags().StringVar(&dryRunFile, "file", "", "deprecated; use positional file")
	var restoreReason string
	restore := &cobra.Command{
		Use: "restore <revision>", Short: "Restore historical policy as a new active revision",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/policy/revisions/{}/restore"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(restoreReason) == "" {
				return errors.New("--reason is required")
			}
			if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
				return errors.New("revision must be an integer")
			}
			return runRender(cmd, http.MethodPost,
				scannerSupplyChainPath+"/policy/revisions/"+url.PathEscape(args[0])+"/restore",
				map[string]any{"reason": restoreReason})
		},
	}
	restore.Flags().StringVar(&restoreReason, "reason", "", "operator reason")
	group.AddCommand(apply, history, dryRun, restore)
	return group
}

func newScannerRegistryCommands() *cobra.Command {
	group := group("registry", "Manage scanner OCI registry targets")
	group.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List scanner registry targets",
			Annotations: apiAnno("GET", scannerSupplyChainPath+"/registries"), Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runRender(cmd, http.MethodGet, scannerSupplyChainPath+"/registries", nil)
			},
		},
		scannerGetByIDCommand("show <id>", "Show scanner registry metadata", scannerSupplyChainPath+"/registries/", apiAnno("GET", scannerSupplyChainPath+"/registries/{}")),
	)
	var name, registryType, host, namespace, secretReference, trustPolicy string
	create := &cobra.Command{
		Use: "create", Short: "Create scanner registry metadata",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || registryType == "" || host == "" {
				return errors.New("--name, --type, and --host are required")
			}
			return runRender(cmd, http.MethodPost, scannerSupplyChainPath+"/registries", map[string]any{
				"name": name, "type": registryType, "host": host, "namespace": namespace,
				"secret_reference": secretReference, "trust_policy_reference": trustPolicy,
				"platform_policy": map[string]any{},
			})
		},
	}
	create.Flags().StringVar(&name, "name", "", "registry display name")
	create.Flags().StringVar(&registryType, "type", "", "managed|mirror|private|air_gap")
	create.Flags().StringVar(&host, "host", "", "registry host[:port]")
	create.Flags().StringVar(&namespace, "namespace", "", "repository namespace")
	create.Flags().StringVar(&secretReference, "secret-reference", "", "credential secret reference")
	create.Flags().StringVar(&trustPolicy, "trust-policy", "", "trust policy reference")
	group.AddCommand(create)

	var patchFile, patchVersion string
	update := &cobra.Command{
		Use: "update <id>", Short: "Update scanner registry metadata",
		Annotations: apiAnno("PATCH", scannerSupplyChainPath+"/registries/{}"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if patchFile == "" || patchVersion == "" {
				return errors.New("--file and --if-match are required")
			}
			body, err := readObjectFile(patchFile)
			if err != nil {
				return err
			}
			return runScannerCommand(cmd, http.MethodPatch,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0]), body, "", patchVersion, false)
		},
	}
	update.Flags().StringVar(&patchFile, "file", "", "JSON/YAML patch file")
	update.Flags().StringVar(&patchVersion, "if-match", "", "current registry version")
	group.AddCommand(update)

	var deleteVersion string
	deleteCommand := &cobra.Command{
		Use: "delete <id>", Short: "Disable scanner registry metadata",
		Annotations: apiAnno("DELETE", scannerSupplyChainPath+"/registries/{}"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deleteVersion == "" {
				return errors.New("--if-match is required")
			}
			return runScannerCommand(cmd, http.MethodDelete,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0]), nil, "", deleteVersion, false)
		},
	}
	deleteCommand.Flags().StringVar(&deleteVersion, "if-match", "", "current registry version")
	group.AddCommand(deleteCommand)
	group.AddCommand(&cobra.Command{
		Use: "check <id>", Short: "Check scanner registry connectivity",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries/{}/check"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, http.MethodPost,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0])+"/check", map[string]any{})
		},
	})
	var releaseID string
	reconcile := &cobra.Command{
		Use: "reconcile <id>", Short: "Compare release digests with registry state",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries/{}/reconcile"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if releaseID == "" {
				return errors.New("--release is required")
			}
			return runRender(cmd, http.MethodPost,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0])+"/reconcile",
				map[string]any{"release_id": releaseID})
		},
	}
	reconcile.Flags().StringVar(&releaseID, "release", "", "release ID to reconcile")
	group.AddCommand(reconcile)

	var repairRelease, repairSource, repairReason, repairPolicy, repairKey string
	var repairMaxAttempts int
	repair := &cobra.Command{
		Use: "repair <id>", Short: "Queue durable scanner registry drift repair",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries/{}/jobs"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if repairRelease == "" || repairSource == "" || repairReason == "" {
				return errors.New("--release, --source, and --reason are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0])+"/jobs",
				map[string]any{
					"kind": "repair", "release_id": repairRelease,
					"source_registry_id": repairSource, "re_sign_policy": repairPolicy,
					"reason": repairReason, "max_attempts": repairMaxAttempts,
				},
				repairKey, "", true,
			)
		},
	}
	repair.Flags().StringVar(&repairRelease, "release", "", "immutable release ID")
	repair.Flags().StringVar(&repairSource, "source", "", "trusted source registry target ID")
	repair.Flags().StringVar(&repairReason, "reason", "", "operator reason")
	repair.Flags().StringVar(&repairPolicy, "re-sign-policy", "preserve", "preserve|required|forbidden")
	repair.Flags().StringVar(&repairKey, "idempotency-key", "", "stable command key")
	repair.Flags().IntVar(&repairMaxAttempts, "max-attempts", 5, "maximum worker attempts")
	group.AddCommand(repair)

	var durableRelease, durableSource, durableReason, durableKey string
	reconcileJob := &cobra.Command{
		Use: "reconcile-job <id>", Short: "Queue durable scanner registry reconciliation",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries/{}/jobs"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if durableRelease == "" || durableReason == "" {
				return errors.New("--release and --reason are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0])+"/jobs",
				map[string]any{
					"kind": "reconcile", "release_id": durableRelease,
					"source_registry_id": durableSource, "reason": durableReason,
				},
				durableKey, "", true,
			)
		},
	}
	reconcileJob.Flags().StringVar(&durableRelease, "release", "", "immutable release ID")
	reconcileJob.Flags().StringVar(&durableSource, "source", "", "optional trusted source registry target ID")
	reconcileJob.Flags().StringVar(&durableReason, "reason", "", "operator reason")
	reconcileJob.Flags().StringVar(&durableKey, "idempotency-key", "", "stable command key")
	group.AddCommand(reconcileJob)

	var cleanupReason, cleanupKey string
	cleanup := &cobra.Command{
		Use: "cleanup <id>", Short: "Queue guarded scanner registry quarantine cleanup",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registries/{}/cleanup-jobs"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cleanupReason == "" {
				return errors.New("--reason is required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/registries/"+url.PathEscape(args[0])+"/cleanup-jobs",
				map[string]any{"reason": cleanupReason}, cleanupKey, "", true,
			)
		},
	}
	cleanup.Flags().StringVar(&cleanupReason, "reason", "", "operator reason")
	cleanup.Flags().StringVar(&cleanupKey, "idempotency-key", "", "stable command key")
	group.AddCommand(cleanup)

	var jobsState, jobsKind, jobsRegistry, jobsRelease string
	var jobsLimit int
	jobs := &cobra.Command{
		Use: "jobs", Short: "List durable scanner registry jobs",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/registry-jobs"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			values := url.Values{}
			if jobsState != "" {
				values.Set("state", jobsState)
			}
			if jobsKind != "" {
				values.Set("kind", jobsKind)
			}
			if jobsRegistry != "" {
				values.Set("registry_target_id", jobsRegistry)
			}
			if jobsRelease != "" {
				values.Set("release_id", jobsRelease)
			}
			if jobsLimit > 0 {
				values.Set("limit", strconv.Itoa(jobsLimit))
			}
			endpoint := scannerSupplyChainPath + "/registry-jobs"
			if encoded := values.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			return runRender(cmd, http.MethodGet, endpoint, nil)
		},
	}
	jobs.Flags().StringVar(&jobsState, "state", "", "queued|claimed|retry|completed|dead_letter|cancelled")
	jobs.Flags().StringVar(&jobsKind, "kind", "", "reconcile|repair|cleanup")
	jobs.Flags().StringVar(&jobsRegistry, "registry", "", "registry target ID")
	jobs.Flags().StringVar(&jobsRelease, "release", "", "release ID")
	jobs.Flags().IntVar(&jobsLimit, "limit", 50, "maximum jobs")
	group.AddCommand(jobs)
	group.AddCommand(scannerGetByIDCommand(
		"job <id>", "Show a durable registry job and image evidence",
		scannerSupplyChainPath+"/registry-jobs/",
		apiAnno("GET", scannerSupplyChainPath+"/registry-jobs/{}"),
	))
	group.AddCommand(scannerEventsCommand(
		"job-events <id>", "Stream durable registry job events",
		scannerSupplyChainPath+"/registry-jobs/",
		apiAnno("GET", scannerSupplyChainPath+"/registry-jobs/{}/events"),
	))

	var retryReason, retryKey, retryVersion string
	retryJob := &cobra.Command{
		Use: "retry-job <id>", Short: "Retry a dead-lettered scanner registry job",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/registry-jobs/{}/retry"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if retryReason == "" || retryVersion == "" {
				return errors.New("--reason and --if-match are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/registry-jobs/"+url.PathEscape(args[0])+"/retry",
				map[string]any{"reason": retryReason}, retryKey, retryVersion, true,
			)
		},
	}
	retryJob.Flags().StringVar(&retryReason, "reason", "", "operator reason")
	retryJob.Flags().StringVar(&retryKey, "idempotency-key", "", "stable command key")
	retryJob.Flags().StringVar(&retryVersion, "if-match", "", "current registry job version")
	group.AddCommand(retryJob)

	var quarantineRegistry, quarantineState string
	var quarantineLimit int
	quarantine := &cobra.Command{
		Use: "quarantine", Short: "List retained scanner registry quarantine objects",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/registry-quarantine"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			values := url.Values{}
			if quarantineRegistry != "" {
				values.Set("registry_target_id", quarantineRegistry)
			}
			if quarantineState != "" {
				values.Set("state", quarantineState)
			}
			if quarantineLimit > 0 {
				values.Set("limit", strconv.Itoa(quarantineLimit))
			}
			endpoint := scannerSupplyChainPath + "/registry-quarantine"
			if encoded := values.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			return runRender(cmd, http.MethodGet, endpoint, nil)
		},
	}
	quarantine.Flags().StringVar(&quarantineRegistry, "registry", "", "registry target ID")
	quarantine.Flags().StringVar(&quarantineState, "state", "", "quarantine state")
	quarantine.Flags().IntVar(&quarantineLimit, "limit", 100, "maximum objects")
	group.AddCommand(quarantine)
	return group
}

func newScannerAuditCommands() *cobra.Command {
	group := group("audit", "Inspect immutable scanner release domain events")
	list := listQueryCommand(
		"list", "List scanner release audit events",
		scannerSupplyChainPath+"/audit", apiAnno("GET", scannerSupplyChainPath+"/audit"),
	)
	var output string
	export := &cobra.Command{
		Use: "export", Short: "Export scanner release audit history as JSONL",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/audit/export"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			raw, err := client.Raw(cmd.Context(), scannerSupplyChainPath+"/audit/export?format=jsonl")
			if err != nil {
				return err
			}
			if output == "" || output == "-" {
				_, err = cmd.OutOrStdout().Write(raw)
				return err
			}
			if err := os.WriteFile(output, raw, 0o600); err != nil {
				return fmt.Errorf("write audit export: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), output)
			return err
		},
	}
	export.Flags().StringVarP(&output, "output", "o", "-", "output path or - for stdout")
	group.AddCommand(list, export)
	return group
}

func newScannerNotificationCommands() *cobra.Command {
	group := group("notification", "Inspect and retry scanner release notifications")
	var state, destinationType, notificationType, cursor string
	var limit int
	list := &cobra.Command{
		Use: "list", Short: "List scanner release notifications and delivery state",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/notifications"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			values := url.Values{}
			if state != "" {
				values.Set("state", state)
			}
			if destinationType != "" {
				values.Set("destination_type", destinationType)
			}
			if notificationType != "" {
				values.Set("notification_type", notificationType)
			}
			if cursor != "" {
				values.Set("cursor", cursor)
			}
			if limit > 0 {
				values.Set("limit", strconv.Itoa(limit))
			}
			endpoint := scannerSupplyChainPath + "/notifications"
			if encoded := values.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			return runRender(cmd, http.MethodGet, endpoint, nil)
		},
	}
	list.Flags().StringVar(&state, "state", "", "pending|delivering|retry|delivered|dead_letter")
	list.Flags().StringVar(&destinationType, "destination-type", "", "ui|webhook|email|siem")
	list.Flags().StringVar(&notificationType, "notification-type", "", "notification event type")
	list.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")
	list.Flags().IntVar(&limit, "limit", 50, "page size")

	show := scannerGetByIDCommand(
		"show <id>", "Show one scanner release notification",
		scannerSupplyChainPath+"/notifications/",
		apiAnno("GET", scannerSupplyChainPath+"/notifications/{}"),
	)

	var reason, key, version string
	retry := &cobra.Command{
		Use: "retry <id>", Short: "Retry a dead-lettered scanner release notification",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/notifications/{}/retry"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" || version == "" {
				return errors.New("--reason and --if-match are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerSupplyChainPath+"/notifications/"+url.PathEscape(args[0])+"/retry",
				map[string]any{"reason": reason}, key, version, false,
			)
		},
	}
	retry.Flags().StringVar(&reason, "reason", "", "auditable operator reason")
	retry.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	retry.Flags().StringVar(&version, "if-match", "", "current notification version")
	group.AddCommand(list, show, retry)
	return group
}

func newScannerAlertCommands() *cobra.Command {
	group := group("alert", "Inspect scanner release operational alerts")
	var state, kind, severity, cursor string
	var limit int
	list := &cobra.Command{
		Use: "list", Short: "List current or historical scanner release alerts",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/alerts"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			values := url.Values{}
			if state != "" {
				values.Set("state", state)
			}
			if kind != "" {
				values.Set("kind", kind)
			}
			if severity != "" {
				values.Set("severity", severity)
			}
			if cursor != "" {
				values.Set("cursor", cursor)
			}
			if limit > 0 {
				values.Set("limit", strconv.Itoa(limit))
			}
			endpoint := scannerSupplyChainPath + "/alerts"
			if encoded := values.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			return runRender(cmd, http.MethodGet, endpoint, nil)
		},
	}
	list.Flags().StringVar(&state, "state", "open", "open|resolved|all")
	list.Flags().StringVar(&kind, "kind", "", "alert kind")
	list.Flags().StringVar(&severity, "severity", "", "warning|critical")
	list.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")
	list.Flags().IntVar(&limit, "limit", 50, "page size")
	show := scannerGetByIDCommand(
		"show <id>", "Show one scanner release alert",
		scannerSupplyChainPath+"/alerts/",
		apiAnno("GET", scannerSupplyChainPath+"/alerts/{}"),
	)
	group.AddCommand(list, show)
	return group
}

func listQueryCommand(use, short, path string, annotations map[string]string) *cobra.Command {
	var state, cursor, query string
	var limit int
	command := &cobra.Command{
		Use: use, Short: short, Annotations: annotations, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			values := url.Values{}
			if state != "" {
				values.Set("state", state)
			}
			if cursor != "" {
				values.Set("cursor", cursor)
			}
			if query != "" {
				values.Set("q", query)
			}
			if limit > 0 {
				values.Set("limit", strconv.Itoa(limit))
			}
			endpoint := path
			if encoded := values.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			return runRender(cmd, http.MethodGet, endpoint, nil)
		},
	}
	command.Flags().StringVar(&state, "state", "", "state/status filter")
	command.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")
	command.Flags().StringVar(&query, "query", "", "search query")
	command.Flags().IntVar(&limit, "limit", 50, "page size")
	return command
}

func scannerGetByIDCommand(use, short, prefix string, annotations map[string]string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Annotations: annotations, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, http.MethodGet, prefix+url.PathEscape(args[0]), nil)
		},
	}
}

func scannerEventsCommand(use, short, prefix string, annotations map[string]string) *cobra.Command {
	var after string
	command := &cobra.Command{
		Use: use, Short: short, Annotations: annotations, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.StreamEvents(
				cmd.Context(), prefix+url.PathEscape(args[0])+"/events", after,
				func(event StreamEvent) { _, _ = fmt.Fprintln(cmd.OutOrStdout(), event.Data) },
			)
			return err
		},
	}
	command.Flags().StringVar(&after, "after", "", "resume after event sequence")
	return command
}

func runScannerCommand(
	cmd *cobra.Command,
	method, path string,
	body any,
	idempotencyKey, ifMatch string,
	watch bool,
) error {
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	if method == http.MethodPost && idempotencyKey != "-" &&
		!strings.Contains(path, "/registries") {
		if idempotencyKey == "" {
			idempotencyKey = uuid.NewString()
		}
		headers["Idempotency-Key"] = idempotencyKey
	}
	if ifMatch != "" {
		headers["If-Match"] = ifMatch
	}
	envelope, err := client.DoWithHeaders(cmd.Context(), method, path, body, headers)
	if err != nil {
		return err
	}
	if envelope.ID != "" {
		envelope.Data, _ = json.Marshal(map[string]any{
			"id": envelope.ID, "state": envelope.State,
			"status_url": envelope.StatusURL, "events_url": envelope.EventsURL,
		})
	}
	if err := Render(cmd.OutOrStdout(), resolveOutput(cmd), envelope); err != nil {
		return err
	}
	if watch && envelope.EventsURL != "" {
		return watchScannerOperation(cmd.Context(), client, envelope, cmd.OutOrStdout())
	}
	return nil
}

func watchScannerOperation(ctx context.Context, client *Client, command *Envelope, output io.Writer) error {
	lastEventID := ""
	failures := 0
	for {
		terminal := false
		next, err := client.StreamEvents(ctx, command.EventsURL, lastEventID, func(event StreamEvent) {
			_, _ = fmt.Fprintln(output, event.Data)
			if scannerTerminalEvent(event.Data) {
				terminal = true
			}
		})
		lastEventID = next
		if terminal {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failures++
			if failures >= 10 {
				return fmt.Errorf("scanner operation event stream failed after %d reconnects: %w", failures, err)
			}
		} else {
			failures = 0
		}
		if command.StatusURL != "" {
			status, pollErr := client.Do(ctx, http.MethodGet, command.StatusURL, nil)
			if pollErr == nil && scannerTerminalData(status.Data) {
				return nil
			}
		}
		delay := time.Duration(1<<minInt(failures, 3)) * time.Second
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func scannerTerminalEvent(data string) bool {
	var event struct {
		NewState string `json:"new_state"`
		State    string `json:"state"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		return false
	}
	return scannerTerminalState(firstNonEmpty(event.NewState, event.State))
}

func scannerTerminalData(data json.RawMessage) bool {
	if len(data) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	var findState func(any) string
	findState = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			if state, ok := typed["state"].(string); ok {
				return state
			}
			for _, key := range []string{"run", "candidate", "release", "rollout"} {
				if nested, exists := typed[key]; exists {
					if state := findState(nested); state != "" {
						return state
					}
				}
			}
		}
		return ""
	}
	return scannerTerminalState(findState(value))
}

func scannerTerminalState(state string) bool {
	switch state {
	case "completed", "cancelled", "rejected", "published", "deprecated",
		"revoked", "failed", "rolled_back", "partial":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func readObjectFile(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if json.Unmarshal(content, &object) == nil && object != nil {
		return object, nil
	}
	if err := yaml.Unmarshal(content, &object); err != nil {
		return nil, fmt.Errorf("decode %s as JSON or YAML: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%s must contain an object", path)
	}
	return object, nil
}
