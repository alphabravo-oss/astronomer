// Node subcommands: list / get / cordon / uncordon / drain /
// label(+label-remove) / taint(+taint-remove) / annotate(+annotate-remove).
//
// These talk to the generated pkg/astroclient SDK via the shared
// newAstroClient foundation helper (client.go). Every cluster-id argument
// is a UUID (the SDK methods take an openapi_types.UUID), so the commands
// parse it up front and surface a clean error if it is malformed. Results
// are rendered honoring the global -o/--output flag through renderSDK.
//
// Style mirrors cluster.go: one constructor per subcommand, grouped under
// a parent "nodes" command exposed by newNodesCmd().

package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// sdkStatusErr turns an unexpected (non-2xx, or missing typed body) SDK
// response into a clean, action-prefixed error suitable for surfacing to
// the operator with a non-zero exit.
//
// It is intentionally self-contained rather than delegating to a
// package-level sdkError helper: sibling command groups currently ship
// conflicting sdkError signatures, so this group formats its own message
// from the HTTP status + raw body to stay decoupled.
func sdkStatusErr(action string, httpResp *http.Response, body []byte) error {
	status := 0
	if httpResp != nil {
		status = httpResp.StatusCode
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("%s: request failed (HTTP %d): %s", action, status, msg)
}

// newNodesCmd is the parent for all node-management subcommands.
//
// NOTE: the originating task referred to this constructor with a
// path-mangled name ("newcmd/astro/nodes.goCmd"), which is not a valid Go
// identifier. It is implemented here as newNodesCmd; the later root-wiring
// integration step should reference this name.
func newNodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "nodes",
		Aliases: []string{"node"},
		Short:   "Inspect and manage cluster nodes",
		Long: `Inspect and manage the Kubernetes nodes of a registered cluster.

All subcommands take the cluster's UUID as their first argument; the
node-scoped ones also take the node name. Mutating actions (cordon,
uncordon, drain, label, taint, annotate) are proxied to the cluster's
agent and require the operator to have node-management permissions.`,
	}
	cmd.AddCommand(
		newNodesListCmd(),
		newNodesGetCmd(),
		newNodesCordonCmd(),
		newNodesUncordonCmd(),
		newNodesDrainCmd(),
		newNodesLabelCmd(),
		newNodesLabelRemoveCmd(),
		newNodesTaintCmd(),
		newNodesTaintRemoveCmd(),
		newNodesAnnotateCmd(),
		newNodesAnnotateRemoveCmd(),
	)
	return cmd
}

// parseClusterID converts the user-supplied cluster-id argument into the
// UUID type the SDK expects, returning a clean error on bad input.
func parseClusterID(arg string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(arg)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid cluster id %q: must be a UUID", arg)
	}
	return id, nil
}

// ---------------------------------------------------------------------
// Read commands
// ---------------------------------------------------------------------

func newNodesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <cluster-id>",
		Short: "List the nodes of a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdNodesWithResponse(cmd.Context(), clusterID)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("list nodes", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newNodesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster-id> <node>",
		Short: "Show one node's details",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdNodesNodeNameWithResponse(cmd.Context(), clusterID, args[1])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("get node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// Lifecycle commands: cordon / uncordon / drain
// ---------------------------------------------------------------------

func newNodesCordonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cordon <cluster-id> <node>",
		Short: "Mark a node unschedulable",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameCordonWithResponse(cmd.Context(), clusterID, args[1])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("cordon node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
}

func newNodesUncordonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uncordon <cluster-id> <node>",
		Short: "Mark a node schedulable again",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameUncordonWithResponse(cmd.Context(), clusterID, args[1])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("uncordon node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
}

func newNodesDrainCmd() *cobra.Command {
	var (
		deleteEmptyDirData bool
		dryRun             bool
		ignoreDaemonsets   bool
		gracePeriod        int64
	)
	cmd := &cobra.Command{
		Use:   "drain <cluster-id> <node>",
		Short: "Evict pods from a node (cordons first)",
		Long: `Drain evicts the node's pods so it can be taken out of service.
Returns 200 when the drain completed synchronously or 202 when it was
accepted and is proceeding asynchronously.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameDrainJSONRequestBody{
				DryRun: &dryRun,
			}
			if cmd.Flags().Changed("delete-emptydir-data") {
				body.DeleteEmptyDirData = &deleteEmptyDirData
			}
			if cmd.Flags().Changed("ignore-daemonsets") {
				body.IgnoreDaemonsets = &ignoreDaemonsets
			}
			if cmd.Flags().Changed("grace-period") {
				body.GracePeriodSeconds = &gracePeriod
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameDrainWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			switch {
			case resp.JSON200 != nil:
				return renderSDK(cmd, resp.JSON200.Data)
			case resp.JSON202 != nil:
				return renderSDK(cmd, resp.JSON202.Data)
			default:
				return sdkStatusErr("drain node", resp.HTTPResponse, resp.Body)
			}
		},
	}
	cmd.Flags().BoolVar(&deleteEmptyDirData, "delete-emptydir-data", false, "continue even if pods use emptyDir volumes (data is lost)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be evicted without evicting")
	cmd.Flags().BoolVar(&ignoreDaemonsets, "ignore-daemonsets", false, "ignore DaemonSet-managed pods")
	cmd.Flags().Int64Var(&gracePeriod, "grace-period", 0, "override pod termination grace period (seconds)")
	return cmd
}

// ---------------------------------------------------------------------
// Labels
// ---------------------------------------------------------------------

func newNodesLabelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "label <cluster-id> <node> <key> [value]",
		Short: "Add or update a label on a node",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameLabelsJSONRequestBody{Key: args[2]}
			if len(args) == 4 {
				body.Value = &args[3]
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameLabelsWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("label node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newNodesLabelRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "label-remove <cluster-id> <node> <key>",
		Short: "Remove a label from a node",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameLabelsRemoveJSONRequestBody{Key: args[2]}
			resp, err := client.PostApiV1NodesClusterIdNodeNameLabelsRemoveWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("remove node label", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

// ---------------------------------------------------------------------
// Taints
// ---------------------------------------------------------------------

func newNodesTaintCmd() *cobra.Command {
	var effect string
	cmd := &cobra.Command{
		Use:   "taint <cluster-id> <node> <key> [value]",
		Short: "Add or update a taint on a node",
		Long: `Add or update a taint. --effect is required and must be one of
NoSchedule, PreferNoSchedule, or NoExecute.`,
		Args: cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			eff, err := parseTaintEffect(effect)
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameTaintsJSONRequestBody{
				Key:    args[2],
				Effect: eff,
			}
			if len(args) == 4 {
				body.Value = &args[3]
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameTaintsWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("taint node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&effect, "effect", "", "taint effect: NoSchedule|PreferNoSchedule|NoExecute (required)")
	_ = cmd.MarkFlagRequired("effect")
	return cmd
}

func newNodesTaintRemoveCmd() *cobra.Command {
	var effect string
	cmd := &cobra.Command{
		Use:   "taint-remove <cluster-id> <node> <key>",
		Short: "Remove a taint from a node",
		Long: `Remove a taint by key. --effect is optional; when supplied it must
be one of NoSchedule, PreferNoSchedule, or NoExecute and only the taint
with that exact effect is removed.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameTaintsRemoveJSONRequestBody{Key: args[2]}
			if cmd.Flags().Changed("effect") {
				eff, err := parseTaintEffect(effect)
				if err != nil {
					return err
				}
				rmEff := astroclient.NodeTaintRemoveRequestEffect(eff)
				body.Effect = &rmEff
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameTaintsRemoveWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("remove node taint", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&effect, "effect", "", "only remove the taint with this effect: NoSchedule|PreferNoSchedule|NoExecute")
	return cmd
}

// parseTaintEffect validates a taint effect string against the SDK enum.
func parseTaintEffect(s string) (astroclient.NodeTaintRequestEffect, error) {
	switch astroclient.NodeTaintRequestEffect(s) {
	case astroclient.NodeTaintRequestEffectNoSchedule:
		return astroclient.NodeTaintRequestEffectNoSchedule, nil
	case astroclient.NodeTaintRequestEffectPreferNoSchedule:
		return astroclient.NodeTaintRequestEffectPreferNoSchedule, nil
	case astroclient.NodeTaintRequestEffectNoExecute:
		return astroclient.NodeTaintRequestEffectNoExecute, nil
	default:
		return "", fmt.Errorf("invalid taint effect %q: want one of NoSchedule, PreferNoSchedule, NoExecute", s)
	}
}

// ---------------------------------------------------------------------
// Annotations
// ---------------------------------------------------------------------

func newNodesAnnotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "annotate <cluster-id> <node> <key> [value]",
		Short: "Add or update an annotation on a node",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameAnnotationsJSONRequestBody{Key: args[2]}
			if len(args) == 4 {
				body.Value = &args[3]
			}
			resp, err := client.PostApiV1NodesClusterIdNodeNameAnnotationsWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("annotate node", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}

func newNodesAnnotateRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "annotate-remove <cluster-id> <node> <key>",
		Short: "Remove an annotation from a node",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseClusterID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1NodesClusterIdNodeNameAnnotationsRemoveJSONRequestBody{Key: args[2]}
			resp, err := client.PostApiV1NodesClusterIdNodeNameAnnotationsRemoveWithResponse(cmd.Context(), clusterID, args[1], body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkStatusErr("remove node annotation", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
}
