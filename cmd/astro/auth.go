// Auth-related commands: login, logout, whoami.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

const cliTokenLifetimeDays = 30

func newLoginCmd() *cobra.Command {
	var serverFlag, userFlag, passwordFlag string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to an Astronomer server",
		Long: `login prompts for username + password (or reads them from
--user / --password / $ASTRO_PASSWORD), establishes a cookie-bound browser
session, and exchanges it for a 30-day API token stored in
~/.config/astronomer/config.yaml.

The persisted token is used for every subsequent astro command and is revoked
remotely by astro logout. Browser access and refresh JWTs are never exposed to
the CLI.`,
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

			previousServer := cfg.ServerURL
			previousToken := cfg.AccessToken
			previousTokenID := cfg.APITokenID
			tokenName := "astro-cli-" + time.Now().UTC().Format("20060102T150405Z")
			issued, err := astrocli.IssuePasswordAPIToken(
				cmd.Context(), server, username, password, tokenName, cliTokenLifetimeDays,
			)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			cfg.ServerURL = server
			cfg.AccessToken = issued.Token
			cfg.APITokenID = issued.ID
			cfg.RefreshToken = ""
			cfg.Username = issued.Username
			if err := astrocli.SaveConfig(cfg); err != nil {
				_ = astrocli.RevokeAPIToken(cmd.Context(), server, issued.Token, issued.ID)
				return fmt.Errorf("persist config: %w", err)
			}
			if err := astrocli.RevokeAPIToken(cmd.Context(), previousServer, previousToken, previousTokenID); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: previous CLI token could not be revoked: %v\n", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s (API token expires in %d days)\n",
				server, issued.Username, cliTokenLifetimeDays)
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
			revokeErr := astrocli.RevokeAPIToken(cmd.Context(), cfg.ServerURL, cfg.AccessToken, cfg.APITokenID)
			cfg.AccessToken = ""
			cfg.APITokenID = ""
			cfg.RefreshToken = ""
			cfg.Username = ""
			if err := astrocli.SaveConfig(cfg); err != nil {
				return err
			}
			if revokeErr != nil {
				return fmt.Errorf("local credentials cleared, but remote API token revocation failed: %w", revokeErr)
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
			payload := map[string]any{
				"server":       cfg.ServerURL,
				"username":     resp.Data.Username,
				"email":        resp.Data.Email,
				"is_superuser": resp.Data.IsSuperuser,
			}
			return render(cmd, payload, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Server: %s\nUser:   %s\nEmail:  %s\nAdmin:  %v\n",
					cfg.ServerURL, resp.Data.Username, resp.Data.Email, resp.Data.IsSuperuser)
				return err
			})
		},
	}
}

// authedClient is the entry point every non-login command uses to grab
// a Client preloaded with the stored bearer token. Returns a helpful
// error when the user hasn't logged in yet so they don't see "401
// Unauthorized" with no context.
//
// A token supplied via --token or $ASTRO_API_TOKEN takes precedence over
// the stored JWT, letting credential-less invocations (CI, automation)
// run without a prior `astro login`. The --server flag is still required
// (or a stored ServerURL) so we know where to point.
func authedClient(cmd *cobra.Command) (*astrocli.Client, *astrocli.Config, error) {
	cfg, err := astrocli.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	server := cfg.ServerURL
	if override, _ := cmd.Root().PersistentFlags().GetString("server"); strings.TrimSpace(override) != "" {
		server = strings.TrimSpace(override)
	}

	token := cfg.AccessToken
	if override := bearerOverride(cmd); override != "" {
		token = override
	}

	if server == "" {
		return nil, nil, fmt.Errorf("no server configured — pass --server <url> or run `astro login` first")
	}
	if token == "" {
		return nil, nil, fmt.Errorf("not logged in — run `astro login --server <url>` first (or pass --token / set ASTRO_API_TOKEN)")
	}
	return astrocli.NewClient(server, token), cfg, nil
}

// bearerOverride returns an explicit bearer token from --token or the
// ASTRO_API_TOKEN env (flag wins), or "" when neither is set.
func bearerOverride(cmd *cobra.Command) string {
	if flagTok, _ := cmd.Root().PersistentFlags().GetString("token"); strings.TrimSpace(flagTok) != "" {
		return strings.TrimSpace(flagTok)
	}
	return strings.TrimSpace(os.Getenv("ASTRO_API_TOKEN"))
}
