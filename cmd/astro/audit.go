// Audit subcommands: audit logs (list/get/export), activity feed,
// alerting channels + events, the API tools index, and extensions
// (list/get/create/validate/enable/disable/sample-manifest).
//
// This group talks to the generated pkg/astroclient SDK via the shared
// newAstroClient / renderSDK helpers in client.go, mirroring cluster.go's
// style. Each subcommand builds the client, calls the typed SDK method,
// checks the HTTP status, and renders the JSON body honoring -o/--output.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newAuditCmd is the "audit" command group. (A later integration step
// wires this into the root command in main.go.)
func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit logs, activity feed, alerting, API tools, and extensions",
	}
	cmd.AddCommand(
		newAuditListCmd(),
		newAuditGetCmd(),
		newAuditExportCmd(),
		newAuditActivityCmd(),
		newAuditAlertingCmd(),
		newAuditToolsCmd(),
		newAuditExtensionsCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// audit list / get / export
// ---------------------------------------------------------------------

func newAuditListCmd() *cobra.Command {
	var limit, offset int
	var actor, resourceType, action, result string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List audit log entries",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.ListAuditLogsParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			if actor != "" {
				params.Actor = &actor
			}
			if resourceType != "" {
				params.ResourceType = &resourceType
			}
			if action != "" {
				params.Action = &action
			}
			if result != "" {
				r := astroclient.ListAuditLogsParamsResult(result)
				params.Result = &r
			}
			resp, err := client.ListAuditLogsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("list audit logs", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (default server-side 20, max 500)")
	cmd.Flags().IntVar(&offset, "offset", 0, "number of items to skip")
	cmd.Flags().StringVar(&actor, "actor", "", "filter by actor (free-text)")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "filter by resource type")
	cmd.Flags().StringVar(&action, "action", "", "filter by action")
	cmd.Flags().StringVar(&result, "result", "", "outcome filter: success|failure|error")
	return cmd
}

func newAuditGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one audit log entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := uuid.Parse(strings.TrimSpace(args[0]))
			if err != nil {
				return fmt.Errorf("invalid audit log id %q: %w", args[0], err)
			}
			resp, err := client.GetAuditLogWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("get audit log", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newAuditExportCmd() *cobra.Command {
	var format, actor, resourceType, action, result string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export audit logs (CSV) to stdout",
		Long: `Streams the raw export body (CSV) to stdout. Redirect to a file:

  astro audit export > audit.csv

Only the csv format is supported server-side today.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.ExportAuditLogsParams{}
			if format != "" {
				f := astroclient.ExportAuditLogsParamsFormat(format)
				params.Format = &f
			}
			if actor != "" {
				params.Actor = &actor
			}
			if resourceType != "" {
				params.ResourceType = &resourceType
			}
			if action != "" {
				params.Action = &action
			}
			if result != "" {
				r := astroclient.ExportAuditLogsParamsResult(result)
				params.Result = &r
			}
			resp, err := client.ExportAuditLogsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			// The export endpoint returns a raw (CSV) body with no typed
			// JSON envelope, so we write the bytes straight through.
			if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
				return sdkStatusError("export audit logs", resp.StatusCode(), resp.Body)
			}
			_, err = cmd.OutOrStdout().Write(resp.Body)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "export format (csv)")
	cmd.Flags().StringVar(&actor, "actor", "", "filter by actor (free-text)")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "filter by resource type")
	cmd.Flags().StringVar(&action, "action", "", "filter by action")
	cmd.Flags().StringVar(&result, "result", "", "outcome filter: success|failure|error")
	return cmd
}

// ---------------------------------------------------------------------
// audit activity (activity feed)
// ---------------------------------------------------------------------

func newAuditActivityCmd() *cobra.Command {
	var limit int
	var legacy bool
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show the recent activity feed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			if legacy {
				resp, err := client.GetActivityFeedLegacyWithResponse(cmd.Context())
				if err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return sdkStatusError("activity feed (legacy)", resp.StatusCode(), resp.Body)
				}
				return renderSDK(cmd, resp.JSON200)
			}
			params := &astroclient.GetApiV1ActivityParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			resp, err := client.GetApiV1ActivityWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("activity feed", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of activity entries")
	cmd.Flags().BoolVar(&legacy, "legacy", false, "use the legacy activity feed endpoint")
	return cmd
}

// ---------------------------------------------------------------------
// audit alerting (channels list/create, events list)
// ---------------------------------------------------------------------

func newAuditAlertingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerting",
		Short: "Alerting channels and events",
	}
	cmd.AddCommand(
		newAuditAlertingChannelsCmd(),
		newAuditAlertingEventsCmd(),
	)
	return cmd
}

func newAuditAlertingChannelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "Alerting channels",
	}
	cmd.AddCommand(
		newAuditAlertingChannelsListCmd(),
		newAuditAlertingChannelsCreateCmd(),
	)
	return cmd
}

func newAuditAlertingChannelsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List alerting channels",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AlertingChannelsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			// This endpoint has no typed JSON envelope in the SDK; decode
			// the raw body generically so -o/table|json|yaml still works.
			return renderRawBody(cmd, "list alerting channels", resp.StatusCode(), resp.Body)
		},
	}
	return cmd
}

func newAuditAlertingChannelsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an alerting channel from a JSON/YAML body",
		Long: `Reads the channel definition from --file (or stdin when --file is "-")
and POSTs it. The SDK exposes this endpoint without a typed request body,
so the file contents are sent as the raw JSON request body.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			// The generated PostApiV1AlertingChannelsWithResponse takes no
			// body parameter, so the channel definition cannot be attached
			// through the typed SDK method. We surface this gap clearly
			// rather than silently sending an empty create.
			_ = file
			_ = client
			return fmt.Errorf("create alerting channel: the generated SDK method " +
				"PostApiV1AlertingChannelsWithResponse accepts no request body, so a " +
				"channel definition cannot be sent through it; regenerate the SDK with a " +
				"typed body (or use the raw HTTP client) to enable this command")
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to channel definition (JSON/YAML), or - for stdin")
	return cmd
}

func newAuditAlertingEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Alerting events",
	}
	cmd.AddCommand(newAuditAlertingEventsListCmd())
	return cmd
}

func newAuditAlertingEventsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List alerting events",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AlertingEventsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("list alerting events", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// audit tools (api tools index)
// ---------------------------------------------------------------------

func newAuditToolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Show the API tools index",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ToolsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("get api tools", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// audit extensions (list/get/create/validate/enable/disable/sample-manifest)
// ---------------------------------------------------------------------

func newAuditExtensionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "extensions",
		Aliases: []string{"extension", "ext"},
		Short:   "Manage platform extensions",
	}
	cmd.AddCommand(
		newAuditExtensionsListCmd(),
		newAuditExtensionsGetCmd(),
		newAuditExtensionsCreateCmd(),
		newAuditExtensionsValidateCmd(),
		newAuditExtensionsEnableCmd(),
		newAuditExtensionsDisableCmd(),
		newAuditExtensionsSampleManifestCmd(),
	)
	return cmd
}

func newAuditExtensionsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed extensions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ExtensionsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil || resp.JSON200.Data == nil {
				return sdkStatusError("list extensions", resp.StatusCode(), resp.Body)
			}
			var items []astroclient.ExtensionRecord
			if resp.JSON200.Data.Items != nil {
				items = *resp.JSON200.Data.Items
			}
			return renderSDK(cmd, items)
		},
	}
	return cmd
}

func newAuditExtensionsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Show one extension by name",
		Long: `The extensions API exposes a list endpoint but no per-name GET in the
generated SDK, so this fetches the full list and selects the named entry
client-side.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ExtensionsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil || resp.JSON200.Data == nil || resp.JSON200.Data.Items == nil {
				return sdkStatusError("get extension", resp.StatusCode(), resp.Body)
			}
			for _, item := range *resp.JSON200.Data.Items {
				if item.Name != nil && *item.Name == args[0] {
					return renderSDK(cmd, item)
				}
			}
			return fmt.Errorf("extension %q not found", args[0])
		},
	}
	return cmd
}

func newAuditExtensionsCreateCmd() *cobra.Command {
	var file string
	var enable bool
	var source string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Install an extension from a manifest file",
		Long: `Reads an extension manifest (JSON or YAML) from --file (or stdin when
--file is "-") and installs it.

  astro audit extensions create --file manifest.yaml --enable`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			manifest, err := loadExtensionManifest(file)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1ExtensionsJSONRequestBody{
				Manifest: manifest,
			}
			if cmd.Flags().Changed("enable") {
				body.Enable = &enable
			}
			if source != "" {
				body.Source = &source
			}
			resp, err := client.PostApiV1ExtensionsWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("create extension", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to extension manifest (JSON/YAML), or - for stdin")
	cmd.Flags().BoolVar(&enable, "enable", false, "enable the extension immediately after install")
	cmd.Flags().StringVar(&source, "source", "", "free-form source tag for the extension")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newAuditExtensionsValidateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate an extension manifest without installing it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			manifest, err := loadExtensionManifest(file)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1ExtensionsValidateJSONRequestBody{
				Manifest: &manifest,
			}
			resp, err := client.PostApiV1ExtensionsValidateWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("validate extension", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to extension manifest (JSON/YAML), or - for stdin")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newAuditExtensionsEnableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable an installed extension",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ExtensionsNameEnableWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("enable extension", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newAuditExtensionsDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable an installed extension",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ExtensionsNameDisableWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("disable extension", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newAuditExtensionsSampleManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sample-manifest",
		Short: "Print a sample extension manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ExtensionsSampleManifestWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusError("sample manifest", resp.StatusCode(), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// loadExtensionManifest reads an extension manifest from path (or stdin
// when path is "-") and unmarshals it as JSON or YAML. YAML is a superset
// of JSON, so the YAML decoder handles both forms.
func loadExtensionManifest(path string) (astroclient.ExtensionManifest, error) {
	var manifest astroclient.ExtensionManifest
	if path == "" {
		return manifest, fmt.Errorf("--file is required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = readAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return manifest, fmt.Errorf("read manifest: %w", err)
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("parse manifest: %w", err)
	}
	return manifest, nil
}

// readAll slurps an io.Reader; small wrapper kept local so the import set
// stays minimal.
func readAll(f *os.File) ([]byte, error) {
	return os.ReadFile(f.Name())
}

// renderRawBody decodes a raw response body as generic JSON and renders it
// honoring -o/--output. Used for endpoints the SDK exposes without a typed
// JSON envelope.
func renderRawBody(cmd *cobra.Command, action string, status int, body []byte) error {
	if status < 200 || status >= 300 {
		return sdkStatusError(action, status, body)
	}
	if len(body) == 0 {
		return renderSDK(cmd, map[string]any{})
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		// Not JSON; emit the raw bytes verbatim.
		_, err := cmd.OutOrStdout().Write(body)
		return err
	}
	return renderSDK(cmd, v)
}

// sdkStatusError builds a clean error for a non-2xx SDK response. main.go
// prints it and exits non-zero (SilenceErrors is set on the root command).
func sdkStatusError(action string, status int, body []byte) error {
	msg := snippet(body)
	if msg == "" {
		return fmt.Errorf("%s: server returned HTTP %d", action, status)
	}
	return fmt.Errorf("%s: server returned HTTP %d: %s", action, status, msg)
}

// snippet trims a response body to a short, single-line message suitable
// for an error string.
func snippet(body []byte) string {
	const max = 300
	s := string(body)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
