package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/fix/qualification"
)

func newFixerQualificationCmd() *cobra.Command {
	var expectedVariant, expectedAuthMode, scratch string
	cmd := &cobra.Command{
		Use:   "qualification",
		Short: "Validate the packaged fixer engine and authentication boundary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(expectedVariant) == "" || strings.TrimSpace(expectedAuthMode) == "" {
				return fmt.Errorf("--expected-variant and --expected-auth-mode are required")
			}
			report, err := qualification.Run(cmd.Context(), expectedVariant, expectedAuthMode, scratch)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetEscapeHTML(false)
			return encoder.Encode(report)
		},
	}
	cmd.Flags().StringVar(&expectedVariant, "expected-variant", "", "exact fixer variant from the immutable release lock")
	cmd.Flags().StringVar(&expectedAuthMode, "expected-auth-mode", "", "exact authentication mode from the immutable release lock")
	cmd.Flags().StringVar(&scratch, "scratch", "/run/wolf-qualification", "executable tmpfs used only for deterministic CLI protocol shims")
	return cmd
}
