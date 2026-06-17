// Monitoring subcommands: cluster metrics, health, conditions,
// vulnerabilities, events, and service-mesh introspection.
//
// Every command here is SDK-backed: it builds the generated
// pkg/astroclient client via the shared newAstroClient foundation helper,
// calls the typed ...WithResponse method, and renders the JSON body
// envelope through renderSDK so the global -o/--output flag (table|json|
// yaml) is honored uniformly. Errors are mapped to a clean message and a
// non-zero exit (cobra propagates the returned error).
//
// Style mirrors cluster.go: one constructor per subcommand, ExactArgs for
// the <cluster-id> positional, and a single RunE closure that does
// client -> SDK call -> render.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newMonitoringCmd is the group constructor. main.go wires this into the
// root command in a later integration step; an unreferenced constructor
// compiles fine in the meantime.
func newMonitoringCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "monitoring",
		Aliases: []string{"mon", "monitor"},
		Short:   "Cluster monitoring: metrics, health, conditions, vulnerabilities, events, service mesh",
	}
	cmd.AddCommand(
		newMonitoringMetricsCmd(),
		newMonitoringMetricsSummaryCmd(),
		newMonitoringHealthCmd(),
		newMonitoringConditionsCmd(),
		newMonitoringConditionRemediationCmd(),
		newMonitoringVulnerabilitiesCmd(),
		newMonitoringEventsCmd(),
		newMonitoringServiceMeshCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// monParseClusterID parses the positional <cluster-id> argument into the
// openapi_types.UUID the SDK methods require, returning a friendly error
// instead of the raw uuid parse failure.
func monParseClusterID(arg string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(arg))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid cluster id %q: must be a UUID", arg)
	}
	return id, nil
}

// sdkError turns a non-2xx SDK response into a clean error. The generated
// envelopes expose HTTPResponse + raw Body; we prefer the server's error
// message when present and otherwise fall back to the HTTP status.
func monSDKError(resp *http.Response, body []byte) error {
	status := "request failed"
	if resp != nil {
		status = resp.Status
	}
	// Try to surface a structured {"error": {"message": ...}} or
	// {"detail": ...} payload the API commonly returns.
	if msg := monExtractErrMessage(body); msg != "" {
		return fmt.Errorf("%s: %s", status, msg)
	}
	if len(bytes.TrimSpace(body)) > 0 {
		return fmt.Errorf("%s: %s", status, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%s", status)
}

// extractErrMessage best-effort pulls a human message out of a JSON error
// body without knowing the exact envelope shape.
func monExtractErrMessage(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	switch {
	case env.Error.Message != "":
		return env.Error.Message
	case env.Message != "":
		return env.Message
	case env.Detail != "":
		return env.Detail
	}
	return ""
}

// ---------------------------------------------------------------------
// metrics
// ---------------------------------------------------------------------

func newMonitoringMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics <cluster-id>",
		Short: "Show raw cluster metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdMetricsWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newMonitoringMetricsSummaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics-summary <cluster-id>",
		Short: "Show summarized cluster metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdMetricsSummaryWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// health
// ---------------------------------------------------------------------

func newMonitoringHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health <cluster-id>",
		Short: "Show cluster health rollup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdHealthWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// conditions / condition-remediation
// ---------------------------------------------------------------------

func newMonitoringConditionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "conditions <cluster-id>",
		Short: "List cluster conditions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdConditionsWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newMonitoringConditionRemediationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "condition-remediation <cluster-id>",
		Short: "Show remediation guidance for a cluster's conditions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdConditionRemediationWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// vulnerabilities (summary | images)
// ---------------------------------------------------------------------

func newMonitoringVulnerabilitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vulnerabilities",
		Aliases: []string{"vulns", "vuln"},
		Short:   "Image vulnerability reporting for a cluster",
	}
	cmd.AddCommand(
		newMonitoringVulnerabilitiesSummaryCmd(),
		newMonitoringVulnerabilitiesImagesCmd(),
	)
	return cmd
}

func newMonitoringVulnerabilitiesSummaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "summary <cluster-id>",
		Short: "Show the cluster's vulnerability summary (severity counts)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdVulnerabilitiesSummaryWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newMonitoringVulnerabilitiesImagesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "images <cluster-id>",
		Short: "List per-image vulnerability findings for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersIdVulnerabilitiesImagesWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// events
// ---------------------------------------------------------------------

func newMonitoringEventsCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "events <cluster-id>",
		Short: "List recent Kubernetes events for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			var params *astroclient.GetApiV1ClustersClusterIdEventsParams
			if cmd.Flags().Changed("limit") {
				params = &astroclient.GetApiV1ClustersClusterIdEventsParams{Limit: &limit}
			}
			resp, err := client.GetApiV1ClustersClusterIdEventsWithResponse(cmd.Context(), id, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max events to return (forwarded to the upstream k8s events list)")
	return cmd
}

// ---------------------------------------------------------------------
// service-mesh (get | inventory | mtls | detect | validate)
// ---------------------------------------------------------------------

func newMonitoringServiceMeshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service-mesh <cluster-id>",
		Aliases: []string{"mesh", "servicemesh"},
		Short:   "Service-mesh introspection for a cluster",
	}
	cmd.AddCommand(
		newServiceMeshGetCmd(),
		newServiceMeshInventoryCmd(),
		newServiceMeshMtlsCmd(),
		newServiceMeshDetectCmd(),
		newServiceMeshValidateCmd(),
	)
	return cmd
}

func newServiceMeshGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster-id>",
		Short: "Show the cluster's detected service-mesh state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdServiceMeshWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newServiceMeshInventoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inventory <cluster-id>",
		Short: "List the service-mesh inventory (sidecars, gateways, policies)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdServiceMeshInventoryWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newServiceMeshMtlsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mtls <cluster-id>",
		Short: "Show the cluster's mTLS coverage breakdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdServiceMeshMtlsWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newServiceMeshDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect <cluster-id>",
		Short: "Trigger a fresh service-mesh detection scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ClustersClusterIdServiceMeshDetectWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// newServiceMeshValidateCmd posts a candidate mesh policy for validation.
//
// The generated request body is a oneOf union ({"yaml": ...} | {"object":
// ...}) with no exported builder methods, so we build the JSON payload
// ourselves and use the WithBody reader variant of the SDK method. The
// operator supplies the policy either as a YAML/JSON file (--file, "-" for
// stdin) or inline (--yaml). A raw YAML document is wrapped as {"yaml":
// "..."}; a JSON object is forwarded as {"object": {...}}.
func newServiceMeshValidateCmd() *cobra.Command {
	var file, yamlInline string
	cmd := &cobra.Command{
		Use:   "validate <cluster-id>",
		Short: "Validate a service-mesh policy document against the cluster",
		Long: `Validate a candidate service-mesh policy.

Supply the policy via one of:
  --file <path>   read the policy document from a file ("-" for stdin)
  --yaml <text>   inline YAML/JSON policy text

A document that parses as a JSON object is sent as {"object": {...}};
anything else is sent verbatim as {"yaml": "<document>"}.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := monParseClusterID(args[0])
			if err != nil {
				return err
			}

			doc, err := readPolicyDoc(cmd, file, yamlInline)
			if err != nil {
				return err
			}

			payload, err := buildValidateBody(doc)
			if err != nil {
				return err
			}

			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ClustersClusterIdServiceMeshValidateWithBodyWithResponse(
				cmd.Context(), id, "application/json", bytes.NewReader(payload))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return monSDKError(resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", `policy document file ("-" for stdin)`)
	cmd.Flags().StringVar(&yamlInline, "yaml", "", "inline policy text (YAML or JSON)")
	return cmd
}

// readPolicyDoc resolves the policy document from --file (or stdin via
// "-") or the inline --yaml flag, requiring exactly one source.
func readPolicyDoc(cmd *cobra.Command, file, inline string) (string, error) {
	switch {
	case file != "" && inline != "":
		return "", fmt.Errorf("provide only one of --file or --yaml")
	case inline != "":
		return inline, nil
	case file == "-":
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read policy from stdin: %w", err)
		}
		return string(b), nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read policy file %q: %w", file, err)
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("a policy document is required: pass --file or --yaml")
	}
}

// buildValidateBody encodes the policy document into the union request
// body the API expects. If the document is a JSON object it is sent as
// {"object": {...}}; otherwise it is sent as {"yaml": "<document>"}.
func buildValidateBody(doc string) ([]byte, error) {
	trimmed := strings.TrimSpace(doc)
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			return json.Marshal(map[string]interface{}{"object": obj})
		}
		// Falls through to the yaml form if it isn't valid JSON after all.
	}
	return json.Marshal(map[string]interface{}{"yaml": doc})
}
