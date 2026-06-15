// Auth-related commands: login, logout, whoami.

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

// loginResponse mirrors the /api/v1/auth/login/ response envelope.
type loginResponse struct {
	Data struct {
		Token   string `json:"token"`
		Refresh string `json:"refresh"`
		User    struct {
			Username    string `json:"username"`
			Email       string `json:"email"`
			IsSuperuser bool   `json:"is_superuser"`
		} `json:"user"`
	} `json:"data"`
}

func newLoginCmd() *cobra.Command {
	var serverFlag, userFlag, passwordFlag string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to an Astronomer server",
		Long: `login prompts for username + password (or reads them from
--user / --password / $ASTRO_PASSWORD), POSTs to /api/v1/auth/login/,
and persists the returned JWT to ~/.config/astronomer/config.yaml.

The persisted token is used for every subsequent astro command.`,
		Example: `  astro login --server https://astronomer.example.com
  astro login --user admin --password "$(pass astronomer/admin)"
  ASTRO_PASSWORD=… astro login --user admin`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --server on `login` falls back to the root --server flag
			// AND the existing config.ServerURL (re-login to the same
			// server is the common case).
			server := strings.TrimSpace(serverFlag)
			if server == "" {
				server, _ = cmd.Root().PersistentFlags().GetString("server")
			}
			cfg, err := astrocli.LoadConfig()
			if err != nil {
				return err
			}
			if server == "" {
				server = cfg.ServerURL
			}
			if server == "" {
				return fmt.Errorf("no server configured: pass --server <url> or set it in ~/.config/astronomer/config.yaml")
			}

			username := strings.TrimSpace(userFlag)
			if username == "" {
				if _, err := fmt.Fprint(os.Stderr, "Username: "); err != nil {
					return err
				}
				if _, err := fmt.Scanln(&username); err != nil {
					return fmt.Errorf("read username: %w", err)
				}
			}

			password := passwordFlag
			if password == "" {
				password = os.Getenv("ASTRO_PASSWORD")
			}
			if password == "" {
				if _, err := fmt.Fprint(os.Stderr, "Password: "); err != nil {
					return err
				}
				pw, err := term.ReadPassword(int(syscall.Stdin))
				if _, werr := fmt.Fprintln(os.Stderr); werr != nil && err == nil {
					err = werr
				}
				if err != nil {
					return fmt.Errorf("read password: %w", err)
				}
				password = string(pw)
			}

			client := astrocli.NewClient(server, "")
			var resp loginResponse
			err = client.Do(cmd.Context(), "POST", "/api/v1/auth/login/", map[string]any{
				"username": username,
				"password": password,
			}, &resp)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
			if resp.Data.Token == "" {
				return fmt.Errorf("login response carried no token — check server compatibility")
			}

			cfg.ServerURL = server
			cfg.AccessToken = resp.Data.Token
			cfg.RefreshToken = resp.Data.Refresh
			cfg.Username = resp.Data.User.Username
			if err := astrocli.SaveConfig(cfg); err != nil {
				return fmt.Errorf("persist config: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s\n", server, resp.Data.User.Username)
			return err
		},
	}
	cmd.Flags().StringVarP(&serverFlag, "server", "s", "", "Astronomer server URL")
	cmd.Flags().StringVarP(&userFlag, "user", "u", "", "username (prompted when omitted)")
	cmd.Flags().StringVar(&passwordFlag, "password", "", "password (prompted when omitted; ASTRO_PASSWORD env also honored)")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear the locally-stored auth token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := astrocli.LoadConfig()
			if err != nil {
				return err
			}
			cfg.AccessToken = ""
			cfg.RefreshToken = ""
			cfg.Username = ""
			if err := astrocli.SaveConfig(cfg); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return err
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the currently-authenticated user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cfg, err := authedClient(cmd)
			if err != nil {
				return err
			}
			// Fetch /auth/me/ so we surface live data (account locked,
			// superuser flag flipped, etc.) rather than stale config.
			var resp struct {
				Data struct {
					Username    string `json:"username"`
					Email       string `json:"email"`
					IsSuperuser bool   `json:"is_superuser"`
				} `json:"data"`
			}
			if err := client.Do(cmd.Context(), "GET", "/api/v1/auth/me/", nil, &resp); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\nUser:   %s\nEmail:  %s\nAdmin:  %v\n",
				cfg.ServerURL, resp.Data.Username, resp.Data.Email, resp.Data.IsSuperuser)
			return err
		},
	}
}

// authedClient is the entry point every non-login command uses to grab
// a Client preloaded with the stored bearer token. Returns a helpful
// error when the user hasn't logged in yet so they don't see "401
// Unauthorized" with no context.
func authedClient(cmd *cobra.Command) (*astrocli.Client, *astrocli.Config, error) {
	cfg, err := astrocli.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	server := cfg.ServerURL
	if override, _ := cmd.Root().PersistentFlags().GetString("server"); strings.TrimSpace(override) != "" {
		server = strings.TrimSpace(override)
	}
	if server == "" || cfg.AccessToken == "" {
		return nil, nil, fmt.Errorf("not logged in — run `astro login --server <url>` first")
	}
	return astrocli.NewClient(server, cfg.AccessToken), cfg, nil
}
