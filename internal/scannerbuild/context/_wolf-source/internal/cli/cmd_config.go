package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newConfigCmd builds the `wolf config` command group — management of the
// CLI's named contexts in ~/.wolf/cli.yaml.
func newConfigCmd() *cobra.Command {
	cmd := group("config", "Manage CLI contexts (server URLs and tokens)")

	var showTokens bool
	view := &cobra.Command{
		Use:   "view",
		Short: "Print the CLI configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "config file: %s\n", ConfigPath())
			fmt.Fprintf(out, "current context: %s\n", emptyDash(cfg.CurrentContext))
			for name, c := range cfg.Contexts {
				tok := "<redacted>"
				if showTokens {
					tok = c.Token
				} else if c.Token == "" {
					tok = "<none>"
				}
				fmt.Fprintf(out, "  %s\tserver=%s token=%s\n", name, c.Server, tok)
			}
			return nil
		},
	}
	view.Flags().BoolVar(&showTokens, "show-tokens", false, "print tokens in clear text")

	var setServer, setToken string
	setContext := &cobra.Command{
		Use:   "set-context <name>",
		Short: "Create or update a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			existing := cfg.Contexts[args[0]]
			if setServer != "" {
				existing.Server = setServer
			}
			if setToken != "" {
				existing.Token = setToken
			}
			cfg.Contexts[args[0]] = existing
			if cfg.CurrentContext == "" {
				cfg.CurrentContext = args[0]
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "context %q saved\n", args[0])
			return nil
		},
	}
	setContext.Flags().StringVar(&setServer, "server", "", "server URL for the context")
	setContext.Flags().StringVar(&setToken, "token", "", "API token for the context")

	useContext := &cobra.Command{
		Use:   "use-context <name>",
		Short: "Switch the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("context %q does not exist", args[0])
			}
			cfg.CurrentContext = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "switched to context %q\n", args[0])
			return nil
		},
	}

	currentContext := &cobra.Command{
		Use:   "current-context",
		Short: "Print the active context name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), emptyDash(cfg.CurrentContext))
			return nil
		},
	}

	deleteContext := &cobra.Command{
		Use:   "delete-context <name>",
		Short: "Remove a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("context %q does not exist", args[0])
			}
			delete(cfg.Contexts, args[0])
			if cfg.CurrentContext == args[0] {
				cfg.CurrentContext = ""
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "context %q deleted\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(view, setContext, useContext, currentContext, deleteContext)
	return cmd
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
