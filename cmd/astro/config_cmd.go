// `astro config` — inspect and edit the on-disk CLI config.
//
// Operates over ~/.config/astronomer/config.yaml via the same
// astrocli.LoadConfig/SaveConfig helpers the rest of the CLI uses, so
// the 0600 file perms (enforced by SaveConfig) are preserved on every
// write. Only the safe, user-settable keys are exposed; API credential fields
// are managed by `astro login` / `astro logout`.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

// configKeys maps the user-facing key names to getters/setters on the
// Config struct. Tokens are intentionally read-only here (set via login).
var configKeys = map[string]struct {
	get      func(*astrocli.Config) string
	set      func(*astrocli.Config, string)
	readOnly bool
}{
	"server_url": {
		get: func(c *astrocli.Config) string { return c.ServerURL },
		set: func(c *astrocli.Config, v string) { c.ServerURL = v },
	},
	"username": {
		get: func(c *astrocli.Config) string { return c.Username },
		set: func(c *astrocli.Config, v string) { c.Username = v },
	},
	"access_token": {
		get:      func(c *astrocli.Config) string { return redactToken(c.AccessToken) },
		readOnly: true,
	},
	"api_token_id": {
		get:      func(c *astrocli.Config) string { return c.APITokenID },
		readOnly: true,
	},
	"refresh_token": {
		get:      func(c *astrocli.Config) string { return redactToken(c.RefreshToken) },
		readOnly: true,
	},
}

func sortedConfigKeys() []string {
	keys := make([]string, 0, len(configKeys))
	for k := range configKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and edit the CLI config (~/.config/astronomer/config.yaml)",
		Long: `config reads and writes the on-disk CLI config. The file is kept
at chmod 0600 because it carries the bearer token.

Settable keys: server_url, username.
Read-only keys (managed by login/logout): access_token, api_token_id,
refresh_token. Secret values are redacted in every output format.`,
	}
	cmd.AddCommand(
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigCurrentCmd(),
	)
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "get <key>",
		Short:     "Print one config value",
		Args:      cobra.ExactArgs(1),
		ValidArgs: sortedConfigKeys(),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := strings.TrimSpace(args[0])
			spec, ok := configKeys[key]
			if !ok {
				return fmt.Errorf("unknown config key %q (valid: %s)", key, strings.Join(sortedConfigKeys(), ", "))
			}
			cfg, err := astrocli.LoadConfig()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), spec.get(cfg))
			return err
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one config value (preserves 0600 perms)",
		Args:  cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				// Only the writable keys are useful completions for `set`.
				var settable []string
				for _, k := range sortedConfigKeys() {
					if !configKeys[k].readOnly {
						settable = append(settable, k)
					}
				}
				return settable, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := strings.TrimSpace(args[0])
			spec, ok := configKeys[key]
			if !ok {
				return fmt.Errorf("unknown config key %q (valid: %s)", key, strings.Join(sortedConfigKeys(), ", "))
			}
			if spec.readOnly {
				return fmt.Errorf("%q is read-only — use `astro login` / `astro logout` to manage it", key)
			}
			cfg, err := astrocli.LoadConfig()
			if err != nil {
				return err
			}
			spec.set(cfg, args[1])
			// SaveConfig writes 0600 and creates the parent dir as needed.
			if err := astrocli.SaveConfig(cfg); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %s\n", key, args[1])
			return err
		},
	}
}

func newConfigCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "current",
		Aliases: []string{"view", "show"},
		Short:   "Show the current config (tokens redacted in table/text)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := astrocli.LoadConfig()
			if err != nil {
				return err
			}
			path, _ := astrocli.ConfigPath()
			// Never expose persisted bearer material through command output. Users
			// that deliberately need the token already control the mode-0600 config.
			payload := map[string]any{
				"path":          path,
				"server_url":    cfg.ServerURL,
				"username":      cfg.Username,
				"access_token":  redactToken(cfg.AccessToken),
				"api_token_id":  cfg.APITokenID,
				"refresh_token": redactToken(cfg.RefreshToken),
			}
			return render(cmd, payload, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "Path:          %s\n", path); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "Server:        %s\n", defaultStr(cfg.ServerURL, "—")); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "User:          %s\n", defaultStr(cfg.Username, "—")); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "Access token:  %s\n", redactToken(cfg.AccessToken)); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "API token ID:  %s\n", defaultStr(cfg.APITokenID, "—")); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "Legacy refresh: %s\n", redactToken(cfg.RefreshToken))
				return err
			})
		},
	}
}

// redactToken shows only that a token is present, never its value.
func redactToken(tok string) string {
	if strings.TrimSpace(tok) == "" {
		return "—"
	}
	return "(set)"
}
