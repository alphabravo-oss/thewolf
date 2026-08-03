package cli

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

const scannerCustomBuildPath = "/scanners/custom-builds"

func newScannerCustomBuildCommands() *cobra.Command {
	command := group(
		"custom-build",
		"Build fixed scanner-image variants through the durable worker queue",
	)
	command.AddCommand(
		listQueryCommand(
			"list", "List durable custom scanner-image builds",
			scannerCustomBuildPath,
			apiAnno("GET", scannerCustomBuildPath),
		),
		scannerGetByIDCommand(
			"show <id>", "Show a custom scanner-image build and its variants",
			scannerCustomBuildPath+"/",
			apiAnno("GET", scannerCustomBuildPath+"/{}"),
		),
		scannerEventsCommand(
			"events <id>", "Stream persisted custom-build logs and terminal state",
			scannerCustomBuildPath+"/",
			apiAnno("GET", scannerCustomBuildPath+"/{}/events"),
		),
	)

	var (
		variants       []string
		platforms      []string
		push           bool
		namespace      string
		credentialID   string
		reason         string
		idempotencyKey string
		watch          bool
	)
	create := &cobra.Command{
		Use:         "create",
		Short:       "Enqueue a durable custom scanner-image build",
		Annotations: apiAnno("POST", scannerCustomBuildPath),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(variants) == 0 || strings.TrimSpace(reason) == "" {
				return errors.New("--variant and --reason are required")
			}
			if push && strings.TrimSpace(credentialID) == "" {
				return errors.New("--credential-secret-id is required with --push")
			}
			body := map[string]any{
				"variants": variants, "push": push, "platforms": platforms,
				"reason": reason,
			}
			if namespace != "" {
				body["namespace"] = namespace
			}
			if credentialID != "" {
				body["credential_secret_id"] = credentialID
			}
			return runScannerCommand(
				cmd, http.MethodPost, scannerCustomBuildPath, body,
				idempotencyKey, "", watch,
			)
		},
	}
	create.Flags().StringSliceVar(
		&variants, "variant", nil,
		"fixed variant: default, jvm, rust, codeql, or all (repeatable)",
	)
	create.Flags().StringSliceVar(
		&platforms, "platform", nil,
		"target platform: linux/amd64 or linux/arm64 (repeatable)",
	)
	create.Flags().BoolVar(&push, "push", false, "publish instead of loading locally")
	create.Flags().StringVar(&namespace, "namespace", "", "registry namespace")
	create.Flags().StringVar(
		&credentialID, "credential-secret-id", "",
		"opaque dockerhub_token secret ID used only by the worker",
	)
	create.Flags().StringVar(&reason, "reason", "", "auditable operator reason")
	create.Flags().StringVar(
		&idempotencyKey, "idempotency-key", "",
		"stable command key (default: generated UUID)",
	)
	create.Flags().BoolVar(&watch, "watch", false, "watch persisted events until terminal")

	command.AddCommand(
		create,
		newScannerCustomBuildMutationCommand("cancel"),
		newScannerCustomBuildMutationCommand("retry"),
	)
	return command
}

func newScannerCustomBuildMutationCommand(action string) *cobra.Command {
	var key, version, reason string
	command := &cobra.Command{
		Use:         action + " <id>",
		Short:       strings.ToUpper(action[:1]) + action[1:] + " a durable custom build",
		Annotations: apiAnno("POST", scannerCustomBuildPath+"/{}/"+action),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version == "" || strings.TrimSpace(reason) == "" {
				return errors.New("--if-match and --reason are required")
			}
			return runScannerCommand(
				cmd, http.MethodPost,
				scannerCustomBuildPath+"/"+url.PathEscape(args[0])+"/"+action,
				map[string]any{"reason": reason}, key, version, false,
			)
		},
	}
	command.Flags().StringVar(&key, "idempotency-key", "", "stable command key")
	command.Flags().StringVar(&version, "if-match", "", "current custom-build version")
	command.Flags().StringVar(&reason, "reason", "", "auditable operator reason")
	return command
}
