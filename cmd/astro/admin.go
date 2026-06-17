// Admin subcommands: platform-operator settings backed by the generated
// pkg/astroclient SDK. Every leaf command builds a typed client via the
// shared newAstroClient helper (client.go), invokes one SDK method, and
// renders the typed JSON body honoring the global -o/--output flag.
//
// Groups:
//   smtp                     get | update | test
//   webhooks                 list | get | create | update | delete | test
//   webhook                  deliveries <id> | retry-delivery <id> <deliveryId>
//   vault                    list | get | create | update | delete | test | health
//   network-policy-templates list | get | create | update | delete
//   key-status
//   emails                   list
//   backup-drill
//   shell-sessions           list
//   agents                   posture
//
// Request bodies for create/update/test operations are accepted as raw
// JSON via --data (or --data @file / --data - for stdin). The SDK exposes
// strongly-typed body structs, but they carry many optional nested fields;
// passing the handler's JSON payload through verbatim keeps the CLI a thin,
// faithful pass-through and avoids a flag explosion. The server validates
// the payload and returns a clean 400 we surface as-is.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

const adminJSONContentType = "application/json"

// newAdminCmd builds the "admin" command group.
//
// (The integration step that wires this into the root command lives in
// main.go and is out of scope for this file.)
func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Platform administration (SMTP, webhooks, vault, network policies, keys)",
		Long: `Administer platform-wide settings as an operator.

These commands target the /api/v1/admin/* endpoints and require an
admin-scoped token. Each subcommand prints its result in the format
selected by the global -o/--output flag (table|json|yaml).`,
	}
	cmd.AddCommand(
		newAdminSmtpCmd(),
		newAdminWebhooksCmd(),
		newAdminWebhookCmd(),
		newAdminVaultCmd(),
		newAdminNetworkPolicyTemplatesCmd(),
		newAdminKeyStatusCmd(),
		newAdminEmailsCmd(),
		newAdminBackupDrillCmd(),
		newAdminShellSessionsCmd(),
		newAdminAgentsCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// adminReadData resolves the value of a --data flag into a raw JSON byte
// slice. Supported forms:
//
//	--data '{"k":"v"}'   inline JSON
//	--data @path/to.json read from a file
//	--data -             read from stdin
func adminReadData(cmd *cobra.Command, raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("--data is required (inline JSON, @file, or - for stdin)")
	}
	switch {
	case raw == "-":
		return io.ReadAll(cmd.InOrStdin())
	case strings.HasPrefix(raw, "@"):
		return os.ReadFile(strings.TrimPrefix(raw, "@"))
	default:
		return []byte(raw), nil
	}
}

// adminParseUUID validates a positional UUID argument and returns a clean
// error mapped to a non-zero exit when malformed.
func adminParseUUID(arg string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(arg))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid id %q: must be a UUID", arg)
	}
	return id, nil
}

// adminBody bundles the WithBody invocation arguments for a create/update
// operation; callers pass the raw JSON reader + content type.
func adminBodyReader(data []byte) io.Reader { return strings.NewReader(string(data)) }

// ---------------------------------------------------------------------
// smtp
// ---------------------------------------------------------------------

func newAdminSmtpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smtp",
		Short: "Inspect and manage platform SMTP settings",
	}
	cmd.AddCommand(newAdminSmtpGetCmd(), newAdminSmtpUpdateCmd(), newAdminSmtpTestCmd())
	return cmd
}

func newAdminSmtpGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the current SMTP settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminSmtpGetWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get SMTP settings", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
}

func newAdminSmtpUpdateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update SMTP settings from a JSON body",
		Long: `Update the platform SMTP settings.

Pass the new settings as JSON via --data. Send "password":"<encrypted>"
to keep the stored password, or "" to clear it.

  astro admin smtp update --data '{"enabled":true,"host":"smtp.example.com","port":587}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.AdminSmtpUpdateWithBodyWithResponse(cmd.Context(), adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("update SMTP settings", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminSmtpTestCmd() *cobra.Command {
	var data, recipient string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send a test email using the current SMTP settings",
		Long: `Send a test email. Provide the recipient either with --recipient
or as a JSON body via --data '{"recipient":"you@example.com"}'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			var body []byte
			if strings.TrimSpace(recipient) != "" {
				body = []byte(fmt.Sprintf(`{"recipient":%q}`, strings.TrimSpace(recipient)))
			} else {
				body, err = adminReadData(cmd, data)
				if err != nil {
					return err
				}
			}
			resp, err := client.AdminSmtpTestWithBodyWithResponse(cmd.Context(), adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("send test email", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	cmd.Flags().StringVar(&recipient, "recipient", "", "recipient email address (shortcut for --data)")
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

// ---------------------------------------------------------------------
// webhooks (collection: list/get/create/update/delete/test)
// ---------------------------------------------------------------------

func newAdminWebhooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhooks",
		Short:   "Manage webhook subscriptions",
		Aliases: []string{"webhook-subscriptions"},
	}
	cmd.AddCommand(
		newAdminWebhooksListCmd(),
		newAdminWebhookGetCmd(),
		newAdminWebhooksCreateCmd(),
		newAdminWebhookUpdateCmd(),
		newAdminWebhookDeleteCmd(),
		newAdminWebhookTestCmd(),
	)
	return cmd
}

func newAdminWebhooksListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List webhook subscriptions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhooksListWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list webhooks", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data.Items)
		},
	}
}

func newAdminWebhookGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one webhook subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhookGetWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get webhook", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newAdminWebhooksCreateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a webhook subscription from a JSON body",
		Long: `Create a webhook subscription.

  astro admin webhooks create --data '{"url":"https://hooks.example/x","events":["cluster.created"]}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhooksCreateWithBodyWithResponse(cmd.Context(), adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return adminStatusErr("create webhook", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminWebhookUpdateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a webhook subscription from a JSON body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhookUpdateWithBodyWithResponse(cmd.Context(), id, adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("update webhook", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminWebhookDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a webhook subscription",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			if !yes && !adminConfirm(cmd, fmt.Sprintf("Delete webhook %s?", args[0])) {
				return fmt.Errorf("aborted")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhookDeleteWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return adminStatusErr("delete webhook", sc, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted webhook %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newAdminWebhookTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <id>",
		Short: "Enqueue a test delivery for a webhook subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhookTestWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return adminStatusErr("test webhook", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON202)
		},
	}
}

// ---------------------------------------------------------------------
// webhook (deliveries / retry-delivery)
// ---------------------------------------------------------------------

func newAdminWebhookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Inspect webhook deliveries",
	}
	cmd.AddCommand(newAdminWebhookDeliveriesCmd(), newAdminWebhookRetryDeliveryCmd())
	return cmd
}

func newAdminWebhookDeliveriesCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "deliveries <id>",
		Short: "List recent deliveries for a webhook subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.AdminWebhookDeliveriesParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.AdminWebhookDeliveriesWithResponse(cmd.Context(), id, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list webhook deliveries", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data.Items)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max deliveries to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}

func newAdminWebhookRetryDeliveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry-delivery <id> <delivery-id>",
		Short: "Retry a failed webhook delivery",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			deliveryID, err := adminParseUUID(args[1])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminWebhookRetryDeliveryWithResponse(cmd.Context(), id, deliveryID)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return adminStatusErr("retry webhook delivery", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON202)
		},
	}
}

// ---------------------------------------------------------------------
// vault (connections)
// ---------------------------------------------------------------------

func newAdminVaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vault",
		Short:   "Manage Vault connections",
		Aliases: []string{"vault-connections"},
	}
	cmd.AddCommand(
		newAdminVaultListCmd(),
		newAdminVaultGetCmd(),
		newAdminVaultCreateCmd(),
		newAdminVaultUpdateCmd(),
		newAdminVaultDeleteCmd(),
		newAdminVaultTestCmd(),
		newAdminVaultHealthCmd(),
	)
	return cmd
}

func newAdminVaultListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Vault connections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionsListWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list vault connections", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data.Items)
		},
	}
}

func newAdminVaultGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one Vault connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionGetWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get vault connection", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newAdminVaultCreateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Vault connection from a JSON body",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionsCreateWithBodyWithResponse(cmd.Context(), adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return adminStatusErr("create vault connection", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminVaultUpdateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a Vault connection from a JSON body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionUpdateWithBodyWithResponse(cmd.Context(), id, adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("update vault connection", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminVaultDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a Vault connection",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			if !yes && !adminConfirm(cmd, fmt.Sprintf("Delete vault connection %s?", args[0])) {
				return fmt.Errorf("aborted")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionDeleteWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return adminStatusErr("delete vault connection", sc, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted vault connection %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newAdminVaultTestCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "test <id>",
		Short: "Test a Vault connection (optional probe_path JSON body)",
		Long: `Probe a Vault connection. Optionally pass a JSON body to override
the probe path:

  astro admin vault test <id> --data '{"probe_path":"_health"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			// Body is optional; default to an empty JSON object so the
			// server applies its _health default.
			body := []byte("{}")
			if strings.TrimSpace(data) != "" {
				if body, err = adminReadData(cmd, data); err != nil {
					return err
				}
			}
			resp, err := client.AdminVaultConnectionTestWithBodyWithResponse(cmd.Context(), id, adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("test vault connection", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "optional JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminVaultHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health <id>",
		Short: "Show health/latency for a Vault connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminVaultConnectionHealthWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get vault connection health", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// network-policy-templates
// ---------------------------------------------------------------------

func newAdminNetworkPolicyTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "network-policy-templates",
		Aliases: []string{"npt", "netpol-templates"},
		Short:   "Manage network policy templates",
	}
	cmd.AddCommand(
		newAdminNPTListCmd(),
		newAdminNPTGetCmd(),
		newAdminNPTCreateCmd(),
		newAdminNPTUpdateCmd(),
		newAdminNPTDeleteCmd(),
	)
	return cmd
}

func newAdminNPTListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List network policy templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1AdminNetworkPolicyTemplatesParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1AdminNetworkPolicyTemplatesWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list network policy templates", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max templates to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}

func newAdminNPTGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one network policy template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AdminNetworkPolicyTemplatesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get network policy template", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newAdminNPTCreateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a network policy template from a JSON body",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AdminNetworkPolicyTemplatesWithBodyWithResponse(cmd.Context(), adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return adminStatusErr("create network policy template", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminNPTUpdateCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a network policy template from a JSON body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body, err := adminReadData(cmd, data)
			if err != nil {
				return err
			}
			resp, err := client.PutApiV1AdminNetworkPolicyTemplatesIdWithBodyWithResponse(cmd.Context(), id, adminJSONContentType, adminBodyReader(body))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("update network policy template", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body (inline, @file, or - for stdin)")
	return cmd
}

func newAdminNPTDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a network policy template",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := adminParseUUID(args[0])
			if err != nil {
				return err
			}
			if !yes && !adminConfirm(cmd, fmt.Sprintf("Delete network policy template %s?", args[0])) {
				return fmt.Errorf("aborted")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1AdminNetworkPolicyTemplatesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return adminStatusErr("delete network policy template", sc, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted network policy template %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ---------------------------------------------------------------------
// key-status
// ---------------------------------------------------------------------

func newAdminKeyStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "key-status",
		Short: "Show encryption/JWT key counts and as-of timestamp",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminKeyStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get key status", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
}

// ---------------------------------------------------------------------
// emails
// ---------------------------------------------------------------------

func newAdminEmailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emails",
		Short: "Inspect the platform email outbox",
	}
	cmd.AddCommand(newAdminEmailsListCmd())
	return cmd
}

func newAdminEmailsListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent emails from the outbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.AdminEmailsListParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.AdminEmailsListWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list emails", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data.Items)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max emails to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}

// ---------------------------------------------------------------------
// backup-drill
// ---------------------------------------------------------------------

func newAdminBackupDrillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup-drill",
		Short: "Run / report the backup restore drill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AdminBackupDrillWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("run backup drill", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
}

// ---------------------------------------------------------------------
// shell-sessions
// ---------------------------------------------------------------------

func newAdminShellSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell-sessions",
		Short: "Inspect cluster shell sessions",
	}
	cmd.AddCommand(newAdminShellSessionsListCmd())
	return cmd
}

func newAdminShellSessionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all cluster shell sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminShellSessionsListWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("list shell sessions", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// agents posture
// ---------------------------------------------------------------------

func newAdminAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Inspect fleet agent administration posture",
	}
	cmd.AddCommand(newAdminAgentsPostureCmd())
	return cmd
}

func newAdminAgentsPostureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "posture",
		Short: "Show per-cluster admin privilege posture across the fleet",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.AdminAgentsClusterAdminPostureWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return adminStatusErr("get agent posture", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// Error / prompt helpers
// ---------------------------------------------------------------------

// adminStatusErr maps a non-2xx SDK response into a clean error. cobra
// turns the returned error into a non-zero exit and prints the message.
// The raw response body is included (trimmed) so the server's structured
// error envelope is visible without dumping a typed struct.
func adminStatusErr(action string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("%s failed: HTTP %d", action, status)
	}
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	return fmt.Errorf("%s failed: HTTP %d: %s", action, status, msg)
}

// adminConfirm prompts the operator for a y/N confirmation, mirroring the
// cluster delete flow.
func adminConfirm(cmd *cobra.Command, prompt string) bool {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt); err != nil {
		return false
	}
	var resp string
	_, _ = fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}
