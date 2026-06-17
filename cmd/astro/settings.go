// Settings subcommands: platform-level settings backed by the generated
// pkg/astroclient SDK.
//
// Groups:
//   get                     full settings snapshot (GET /api/v1/settings)
//   general get|set         general settings (GET/PUT /api/v1/settings/general)
//   features                feature flags (GET /api/v1/settings/features)
//   tokens list|create|delete   API tokens
//   sso list|create|delete      SSO providers
//   audit-logs              settings audit-log entries
//
// Every subcommand builds the typed client via newAstroClient (client.go),
// calls the matching SDK *WithResponse method, maps any non-2xx envelope to
// a clean error (so cobra exits non-zero), and renders the typed JSON body
// through renderSDK so -o/--output (table|json|yaml) is honored.

package main

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "settings",
		Aliases: []string{"setting"},
		Short:   "Manage platform settings",
	}
	cmd.AddCommand(
		newSettingsGetCmd(),
		newSettingsGeneralCmd(),
		newSettingsFeaturesCmd(),
		newSettingsTokensCmd(),
		newSettingsSsoCmd(),
		newSettingsAuditLogsCmd(),
	)
	return cmd
}

// settingsErr converts a non-2xx SDK response into a clean error. It prefers
// the structured ErrorEnvelope body (code + message) when the API returned
// one, falling back to the HTTP status line. Returning a non-nil error makes
// cobra exit non-zero.
func settingsErr(status string, env *astroclient.ErrorEnvelope) error {
	if env != nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("%s", env.Error.Message)
	}
	if status == "" {
		status = "request failed"
	}
	return fmt.Errorf("server returned %s", status)
}

// ---------------------------------------------------------------------------
// settings get (full snapshot)
// ---------------------------------------------------------------------------

func newSettingsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the full platform settings snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1SettingsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------------
// settings general get|set
// ---------------------------------------------------------------------------

func newSettingsGeneralCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "general",
		Short: "View or update general settings",
	}
	cmd.AddCommand(newSettingsGeneralGetCmd(), newSettingsGeneralSetCmd())
	return cmd
}

func newSettingsGeneralGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show general settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1SettingsGeneralWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newSettingsGeneralSetCmd() *cobra.Command {
	var (
		platformName       string
		heartbeatInterval  int
		sessionTimeout     int
		enableAuditLogging bool
		metricsCollection  bool
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update general settings (only flags you pass are changed)",
		Long: `set issues a PUT to /api/v1/settings/general. Only the fields you
explicitly pass as flags are included in the request body; omitted flags are
left untouched server-side.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}

			var body astroclient.PutApiV1SettingsGeneralJSONRequestBody
			changed := false
			if cmd.Flags().Changed("platform-name") {
				v := platformName
				body.PlatformName = &v
				changed = true
			}
			if cmd.Flags().Changed("agent-heartbeat-interval") {
				v := heartbeatInterval
				body.AgentHeartbeatInterval = &v
				changed = true
			}
			if cmd.Flags().Changed("default-session-timeout") {
				v := sessionTimeout
				body.DefaultSessionTimeout = &v
				changed = true
			}
			if cmd.Flags().Changed("enable-audit-logging") {
				v := enableAuditLogging
				body.EnableAuditLogging = &v
				changed = true
			}
			if cmd.Flags().Changed("metrics-collection") {
				v := metricsCollection
				body.MetricsCollection = &v
				changed = true
			}
			if !changed {
				return fmt.Errorf("nothing to update: pass at least one of --platform-name, --agent-heartbeat-interval, --default-session-timeout, --enable-audit-logging, --metrics-collection")
			}

			resp, err := client.PutApiV1SettingsGeneralWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&platformName, "platform-name", "", "platform display name")
	cmd.Flags().IntVar(&heartbeatInterval, "agent-heartbeat-interval", 0, "agent heartbeat interval (seconds)")
	cmd.Flags().IntVar(&sessionTimeout, "default-session-timeout", 0, "default session timeout (seconds)")
	cmd.Flags().BoolVar(&enableAuditLogging, "enable-audit-logging", false, "enable audit logging")
	cmd.Flags().BoolVar(&metricsCollection, "metrics-collection", false, "enable metrics collection")
	return cmd
}

// ---------------------------------------------------------------------------
// settings features
// ---------------------------------------------------------------------------

func newSettingsFeaturesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "features",
		Short: "Show platform feature flags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1SettingsFeaturesWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				// 401 carries an Unauthorized body but not an ErrorEnvelope;
				// fall back to the status line for a clean message.
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------------
// settings tokens list|create|delete
// ---------------------------------------------------------------------------

func newSettingsTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tokens",
		Aliases: []string{"token"},
		Short:   "Manage settings API tokens",
	}
	cmd.AddCommand(
		newSettingsTokensListCmd(),
		newSettingsTokensCreateCmd(),
		newSettingsTokensDeleteCmd(),
	)
	return cmd
}

func newSettingsTokensListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1SettingsTokensParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1SettingsTokensWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of tokens to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}

func newSettingsTokensCreateCmd() *cobra.Command {
	var (
		expiresInDays int
		allowedCidrs  string
		scopes        []string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API token (the plaintext token is returned once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1SettingsTokensJSONRequestBody{
				Name: args[0],
			}
			if cmd.Flags().Changed("expires-in-days") {
				v := expiresInDays
				body.ExpiresInDays = &v
			}
			if cmd.Flags().Changed("allowed-cidrs") {
				v := allowedCidrs
				body.AllowedCidrs = &v
			}
			if len(scopes) > 0 {
				s := scopes
				body.Scopes = &s
			}
			resp, err := client.PostApiV1SettingsTokensWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				switch {
				case resp.JSON400 != nil:
					return settingsErr(resp.Status(), resp.JSON400)
				case resp.JSON429 != nil:
					return settingsErr(resp.Status(), resp.JSON429)
				default:
					return settingsErr(resp.Status(), nil)
				}
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().IntVar(&expiresInDays, "expires-in-days", 0, "token lifetime in days (0 = never expires)")
	cmd.Flags().StringVar(&allowedCidrs, "allowed-cidrs", "", "comma-separated CIDR/IP allowlist")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "token scope (repeatable)")
	return cmd
}

func newSettingsTokensDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm", "revoke"},
		Short:   "Delete (revoke) an API token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid token id %q: %w", args[0], err)
			}
			resp, err := client.DeleteApiV1SettingsTokensIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				if resp.JSON404 != nil {
					return settingsErr(resp.Status(), resp.JSON404)
				}
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// settings sso list|create|delete
// ---------------------------------------------------------------------------

func newSettingsSsoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sso",
		Short: "Manage SSO providers",
	}
	cmd.AddCommand(
		newSettingsSsoListCmd(),
		newSettingsSsoCreateCmd(),
		newSettingsSsoDeleteCmd(),
	)
	return cmd
}

func newSettingsSsoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List SSO providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1SettingsSsoWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
}

func newSettingsSsoCreateCmd() *cobra.Command {
	var (
		providerType         string
		clientID             string
		clientSecret         string
		metadataURL          string
		allowedOrganizations string
		autoCreateUsers      bool
		enabled              bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an SSO provider",
		Long: `create posts to /api/v1/settings/sso. --type is required and must be one
of github, google, or oidc. --client-id and --client-secret are required by
the API. For oidc providers, pass --metadata-url.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}

			switch astroclient.PostApiV1SettingsSsoJSONBodyType(providerType) {
			case astroclient.PostApiV1SettingsSsoJSONBodyTypeGithub,
				astroclient.PostApiV1SettingsSsoJSONBodyTypeGoogle,
				astroclient.PostApiV1SettingsSsoJSONBodyTypeOidc:
				// ok
			default:
				return fmt.Errorf("invalid --type %q: want one of github, google, oidc", providerType)
			}

			var body astroclient.PostApiV1SettingsSsoJSONRequestBody
			body.Name = args[0]
			body.Type = astroclient.PostApiV1SettingsSsoJSONBodyType(providerType)
			body.Config.ClientId = clientID
			body.Config.ClientSecret = clientSecret
			if cmd.Flags().Changed("metadata-url") {
				v := metadataURL
				body.Config.MetadataUrl = &v
			}
			if cmd.Flags().Changed("allowed-organizations") {
				v := allowedOrganizations
				body.Config.AllowedOrganizations = &v
			}
			if cmd.Flags().Changed("auto-create-users") {
				v := autoCreateUsers
				body.Config.AutoCreateUsers = &v
			}
			if cmd.Flags().Changed("enabled") {
				v := enabled
				body.Enabled = &v
			}

			resp, err := client.PostApiV1SettingsSsoWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				switch {
				case resp.JSON400 != nil:
					return settingsErr(resp.Status(), resp.JSON400)
				case resp.JSON409 != nil:
					return settingsErr(resp.Status(), resp.JSON409)
				default:
					return settingsErr(resp.Status(), nil)
				}
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&providerType, "type", "", "provider type: github|google|oidc (required)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client id (required)")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "OAuth client secret (required)")
	cmd.Flags().StringVar(&metadataURL, "metadata-url", "", "OIDC metadata URL (required for oidc)")
	cmd.Flags().StringVar(&allowedOrganizations, "allowed-organizations", "", "comma-separated allowed organizations")
	cmd.Flags().BoolVar(&autoCreateUsers, "auto-create-users", true, "auto-create users on first login")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable the provider")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("client-id")
	_ = cmd.MarkFlagRequired("client-secret")
	return cmd
}

func newSettingsSsoDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete an SSO provider",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid sso provider id %q: %w", args[0], err)
			}
			resp, err := client.DeleteApiV1SettingsSsoIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			// 204/200 with no JSON404 body means success.
			if resp.JSON404 != nil {
				return settingsErr(resp.Status(), resp.JSON404)
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return settingsErr(resp.Status(), nil)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted SSO provider %s\n", id.String())
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// settings audit-logs
// ---------------------------------------------------------------------------

func newSettingsAuditLogsCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "audit-logs",
		Aliases: []string{"audit-log", "audit"},
		Short:   "Show settings audit-log entries",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1SettingsAuditLogsParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1SettingsAuditLogsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return settingsErr(resp.Status(), nil)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of entries to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}
