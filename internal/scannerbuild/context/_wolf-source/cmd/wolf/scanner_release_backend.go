package main

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
)

func newScannerReleaseBackendCmd() *cobra.Command {
	var (
		backend   string
		platforms []string
	)
	command := &cobra.Command{
		Use:   "scanner-release-backend",
		Short: "Inspect built-in scanner release execution backends",
	}
	capabilities := &cobra.Command{
		Use:   "capabilities",
		Short: "Validate backend configuration and print its secure capabilities",
		RunE: func(cmd *cobra.Command, _ []string) error {
			configured, err := scannerreleasebackend.FromEnvironment(backend, platforms)
			if err != nil {
				return err
			}
			advertisement, err := configured.Capabilities(cmd.Context())
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(advertisement); err != nil {
				return fmt.Errorf("encode scanner release backend capabilities: %w", err)
			}
			return nil
		},
	}
	capabilities.Flags().StringVar(
		&backend, "backend",
		envOr("WOLF_SCANNER_RELEASE_EXECUTOR_BACKEND", "local"),
		"built-in backend: local, buildx, or kubernetes-job",
	)
	capabilities.Flags().StringSliceVar(
		&platforms, "platform",
		[]string{runtime.GOOS + "/" + runtime.GOARCH},
		"supported platform (repeatable)",
	)
	command.AddCommand(capabilities)
	return command
}
