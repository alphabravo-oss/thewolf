package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func authRefreshCmd() *cobra.Command {
	return &cobra.Command{Use: "refresh", Short: "Rotate the saved login credentials", Args: cobra.NoArgs,
		Annotations: apiAnno("POST", "/auth/refresh"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			name, _ := cmd.Flags().GetString("context")
			if name == "" {
				name = cfg.CurrentContext
			}
			active, ok := cfg.Active(name)
			if !ok || active.RefreshToken == "" {
				return fmt.Errorf("no refresh credential in context %q; sign in again", name)
			}
			server, _ := cmd.Flags().GetString("server")
			if server != "" && strings.TrimRight(server, "/") != strings.TrimRight(active.Server, "/") {
				return fmt.Errorf("refresh must use the server saved in context %q", name)
			}
			env, err := NewClient(active.Server, "").Do(cmd.Context(), "POST", "/auth/refresh", map[string]string{"refresh_token": active.RefreshToken})
			if err != nil {
				return err
			}
			var pair struct {
				Access  string `json:"access_token"`
				Refresh string `json:"refresh_token"`
			}
			if err := json.Unmarshal(env.Data, &pair); err != nil {
				return err
			}
			if pair.Access == "" || pair.Refresh == "" {
				return fmt.Errorf("server returned incomplete refresh credentials; sign in again")
			}
			active.Token, active.RefreshToken = pair.Access, pair.Refresh
			cfg.Contexts[name] = active
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "credentials refreshed in context %q\n", name)
			return nil
		},
	}
}
