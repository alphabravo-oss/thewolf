package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Process exit codes. AI agents and scripts branch on these.
const (
	ExitOK       = 0
	ExitRuntime  = 1
	ExitUsage    = 2
	ExitNotFound = 3
	ExitAuth     = 4
)

// AddGlobalFlags registers the persistent flags shared by every command.
func AddGlobalFlags(root *cobra.Command) {
	f := root.PersistentFlags()
	f.String("server", "", "wolf API server URL (overrides context/env)")
	f.String("token", "", "API token or JWT (overrides context/env)")
	f.String("context", "", "named context from ~/.wolf/cli.yaml")
	f.StringP("output", "o", "", "output format: json|yaml|table (default: table on a TTY, json when piped)")
}

// ExitCodeFor maps an error returned by a command to a process exit code.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	if ae, ok := err.(*APIError); ok {
		switch {
		case ae.StatusCode == 401 || ae.StatusCode == 403:
			return ExitAuth
		case ae.StatusCode == 404:
			return ExitNotFound
		default:
			return ExitRuntime
		}
	}
	return ExitRuntime
}

// resolveClient builds an API client from, in precedence order: explicit
// flags, the named --context, WOLF_SERVER/WOLF_TOKEN env vars, and finally
// the current context in ~/.wolf/cli.yaml.
func resolveClient(cmd *cobra.Command) (*Client, error) {
	server, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	ctxName, _ := cmd.Flags().GetString("context")

	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if ctxName != "" {
		c, ok := cfg.Contexts[ctxName]
		if !ok {
			return nil, fmt.Errorf("context %q not found in %s", ctxName, ConfigPath())
		}
		if server == "" {
			server = c.Server
		}
		if token == "" {
			token = c.Token
		}
	}
	if server == "" {
		server = os.Getenv("WOLF_SERVER")
	}
	if token == "" {
		token = os.Getenv("WOLF_TOKEN")
	}
	if server == "" || token == "" {
		if c, ok := cfg.Active(""); ok {
			if server == "" {
				server = c.Server
			}
			if token == "" {
				token = c.Token
			}
		}
	}
	if server == "" {
		server = DefaultServer
	}
	return NewClient(server, token), nil
}

// resolveOutput picks the output format: the -o flag if set, otherwise
// table on an interactive terminal and json when stdout is piped.
func resolveOutput(cmd *cobra.Command) string {
	if o, _ := cmd.Flags().GetString("output"); o != "" {
		return o
	}
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return OutputTable
	}
	return OutputJSON
}

// runRender performs an API call and renders the response in the resolved
// output format. It is the backbone of nearly every management command.
func runRender(cmd *cobra.Command, method, path string, body any) error {
	c, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	env, err := c.Do(cmd.Context(), method, path, body)
	if err != nil {
		return err
	}
	return Render(cmd.OutOrStdout(), resolveOutput(cmd), env)
}

// NewCommandGroups returns every API-client command group except scan and
// loop, whose API subcommands are attached to the existing local commands
// via AddScanSubcommands and AddLoopSubcommands. The caller (cmd/wolf) adds
// these to the root.
func NewCommandGroups() []*cobra.Command {
	return []*cobra.Command{
		newConfigCmd(),
		newAuthCmd(),
		newRepoCmd(),
		newNodeCmd(),
		newCollectionCmd(),
		newFindingCmd(),
		newFixCmd(),
		newUserCmd(),
		newSettingsCmd(),
		newPromptCmd(),
		newProviderCmd(),
		newSecretCmd(),
		newPluginCmd(),
		newScannerCmd(),
		newAuditCmd(),
		newSystemCmd(),
	}
}
