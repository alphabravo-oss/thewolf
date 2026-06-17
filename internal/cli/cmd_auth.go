package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newAuthCmd builds the `wolf auth` group: interactive session login for
// humans, plus API-token management for automation.
func newAuthCmd() *cobra.Command {
	cmd := group("auth", "Authenticate and manage API tokens")
	cmd.AddCommand(
		authLoginCmd(),
		authLogoutCmd(),
		authWhoamiCmd(),
		authProfileCmd(),
		authPasswdCmd(),
		authTokenCmd(),
	)
	return cmd
}

// authProfileCmd updates the caller's display name and/or email. Changing the
// email requires the current password (it's the login identifier).
func authProfileCmd() *cobra.Command {
	var name, email, currentPw string
	c := &cobra.Command{
		Use:         "profile",
		Short:       "Update your display name and email",
		Annotations: apiAnno("PUT", "/auth/profile"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("name") && !cmd.Flags().Changed("email") {
				return fmt.Errorf("set --name and/or --email")
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["display_name"] = name
			}
			if cmd.Flags().Changed("email") {
				body["email"] = email
				body["current_password"] = currentPw
			}
			return runRender(cmd, "PUT", "/auth/profile", body)
		},
	}
	c.Flags().StringVar(&name, "name", "", "new display name")
	c.Flags().StringVar(&email, "email", "", "new email (requires --current-password)")
	c.Flags().StringVar(&currentPw, "current-password", "", "current password (required to change email)")
	return c
}

func authLoginCmd() *cobra.Command {
	var email, password, ctxName string
	c := &cobra.Command{
		Use:         "login",
		Short:       "Log in with email + password and store a JWT in a context",
		Annotations: apiAnno("POST", "/auth/login"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server, _ := cmd.Flags().GetString("server")
			if server == "" {
				return fmt.Errorf("--server is required for login")
			}
			if email == "" {
				return fmt.Errorf("--email is required")
			}
			if password == "" {
				fmt.Fprint(cmd.OutOrStdout(), "Password: ")
				sc := bufio.NewScanner(cmd.InOrStdin())
				if sc.Scan() {
					password = strings.TrimSpace(sc.Text())
				}
			}
			client := NewClient(server, "")
			env, err := client.Do(cmd.Context(), "POST", "/auth/login", map[string]string{
				"email": email, "password": password,
			})
			if err != nil {
				return err
			}
			var data struct {
				AccessToken string `json:"access_token"`
				MFARequired bool   `json:"mfa_required"`
				MFAToken    string `json:"mfa_token"`
			}
			if err := json.Unmarshal(env.Data, &data); err != nil {
				return fmt.Errorf("unexpected login response: %w", err)
			}
			// Two-factor: exchange the challenge + a TOTP/recovery code for a
			// session via /auth/mfa/login.
			if data.MFARequired && data.MFAToken != "" {
				fmt.Fprint(cmd.OutOrStdout(), "Two-factor code: ")
				code := ""
				sc := bufio.NewScanner(cmd.InOrStdin())
				if sc.Scan() {
					code = strings.TrimSpace(sc.Text())
				}
				env, err = client.Do(cmd.Context(), "POST", "/auth/mfa/login", map[string]string{
					"mfa_token": data.MFAToken, "code": code,
				})
				if err != nil {
					return err
				}
				if err := json.Unmarshal(env.Data, &data); err != nil {
					return fmt.Errorf("unexpected mfa response: %w", err)
				}
			}
			if data.AccessToken == "" {
				return fmt.Errorf("login succeeded but no access token was returned")
			}
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if ctxName == "" {
				ctxName = "default"
			}
			cfg.Contexts[ctxName] = Context{Server: server, Token: data.AccessToken}
			cfg.CurrentContext = ctxName
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged in; JWT stored in context %q\n", ctxName)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "account email")
	c.Flags().StringVar(&password, "password", "", "account password (prompted if omitted)")
	c.Flags().StringVar(&ctxName, "save-context", "", "context to store the credential in (default: \"default\")")
	return c
}

func authLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "logout",
		Short:       "Log out and clear the active context's credential",
		Annotations: apiAnno("POST", "/auth/logout"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			// Best-effort server-side logout; ignore its result.
			_, _ = c.Do(cmd.Context(), "POST", "/auth/logout", nil)
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if active, ok := cfg.Active(""); ok {
				active.Token = ""
				cfg.Contexts[cfg.CurrentContext] = active
				if err := cfg.Save(); err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
		},
	}
}

func authWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "whoami",
		Short:       "Show the current identity",
		Annotations: apiAnno("GET", "/auth/me"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd, "GET", "/auth/me", nil)
		},
	}
}

func authPasswdCmd() *cobra.Command {
	var current, newPass string
	c := &cobra.Command{
		Use:         "passwd",
		Short:       "Change the current user's password",
		Annotations: apiAnno("PUT", "/auth/password"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if current == "" || newPass == "" {
				return fmt.Errorf("--current and --new are required")
			}
			return runRender(cmd, "PUT", "/auth/password", map[string]string{
				"current_password": current, "new_password": newPass,
			})
		},
	}
	c.Flags().StringVar(&current, "current", "", "current password")
	c.Flags().StringVar(&newPass, "new", "", "new password")
	return c
}

func authTokenCmd() *cobra.Command {
	tok := group("token", "Manage API tokens for non-interactive access")

	var name string
	var scopes []string
	var expiresIn int
	create := &cobra.Command{
		Use:         "create",
		Short:       "Mint an API token (the secret is shown once)",
		Annotations: apiAnno("POST", "/auth/tokens"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if len(scopes) == 0 {
				return fmt.Errorf("at least one --scope is required (or use --scope full)")
			}
			body := map[string]any{"name": name, "scopes": scopes}
			if cmd.Flags().Changed("expires-in") {
				body["expires_in_days"] = expiresIn
			}
			return runRender(cmd, "POST", "/auth/tokens", body)
		},
	}
	create.Flags().StringVar(&name, "name", "", "human label for the token")
	create.Flags().StringArrayVar(&scopes, "scope", nil, "a scope (repeatable); accepts read-only / full aliases")
	create.Flags().IntVar(&expiresIn, "expires-in", 0, "days until expiry (0 = never; omit for the 90-day default)")

	tok.AddCommand(
		create,
		listCmd("/auth/tokens", "List the caller's API tokens"),
		deleteCmd("revoke <id>", "Revoke an API token", "/auth/tokens/%s"),
	)
	return tok
}
