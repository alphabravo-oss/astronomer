// Workloads subcommands: list / get / pods / delete / scale / restart,
// pod logs + delete, and the cross-cluster workload-operations queue.
//
// This group is SDK-backed: every command builds the generated
// pkg/astroclient via the shared newAstroClient helper, calls the typed
// method, and renders the typed JSON body honoring the global -o/--output
// flag (renderSDK for the generic path, render(...) for bespoke tables).
//
// Errors from the SDK layer are mapped to a single clean message via
// sdkError so the command exits non-zero with a useful line instead of a
// raw envelope dump.

package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func newWorkloadsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workloads",
		Aliases: []string{"workload", "wl"},
		Short:   "Inspect and act on cluster workloads (deployments, statefulsets, jobs, ...)",
	}
	cmd.AddCommand(
		newWorkloadsListCmd(),
		newWorkloadsGetCmd(),
		newWorkloadsPodsCmd(),
		newWorkloadsDeleteCmd(),
		newWorkloadsScaleCmd(),
		newWorkloadsRestartCmd(),
		newWorkloadsLogsCmd(),
		newWorkloadsPodCmd(),
		newWorkloadsOperationsCmd(),
		newWorkloadsControllerStatusCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// list <cluster-id>
// ---------------------------------------------------------------------

func newWorkloadsListCmd() *cobra.Command {
	var namespace, kind, search string
	cmd := &cobra.Command{
		Use:   "list <cluster-id>",
		Short: "List workloads on a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1ClustersClusterIdWorkloadsParams{}
			if namespace != "" {
				params.Namespace = &namespace
			}
			if kind != "" {
				params.Kind = &kind
			}
			if search != "" {
				params.Search = &search
			}
			resp, err := client.GetApiV1ClustersClusterIdWorkloadsWithResponse(cmd.Context(), clusterID, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			data := resp.JSON200.Data
			return render(cmd, data, func(w io.Writer) error {
				return writeWorkloadTable(w, data)
			})
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "restrict to a single namespace")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind (Deployment, StatefulSet, DaemonSet, Job, CronJob)")
	cmd.Flags().StringVar(&search, "search", "", "case-insensitive substring match on name or namespace")
	return cmd
}

// ---------------------------------------------------------------------
// get <cluster-id> <kind> <ns> <name>
// ---------------------------------------------------------------------

func newWorkloadsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <cluster-id> <kind> <namespace> <name>",
		Short: "Show one workload's details",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ClustersClusterIdWorkloadsKindNamespaceNameWithResponse(
				cmd.Context(), clusterID, args[1], args[2], args[3])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			wl := resp.JSON200.Data
			return render(cmd, wl, func(w io.Writer) error {
				return writeWorkloadTable(w, []astroclient.Workload{wl})
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// pods <cluster-id> [kind ns name]
//
// With no kind/ns/name => cluster-wide pod listing (optionally scoped to
// a namespace via -n). With kind/ns/name => pods owned by that workload.
// ---------------------------------------------------------------------

func newWorkloadsPodsCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "pods <cluster-id> [<kind> <namespace> <name>]",
		Short: "List pods on a cluster, or pods owned by a specific workload",
		Args:  cobra.RangeArgs(1, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 && len(args) != 4 {
				return fmt.Errorf("expected either <cluster-id> or <cluster-id> <kind> <namespace> <name>")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}

			if len(args) == 4 {
				resp, err := client.GetApiV1ClustersClusterIdWorkloadsKindNamespaceNamePodsWithResponse(
					cmd.Context(), clusterID, args[1], args[2], args[3])
				if err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return workloadSDKError(resp.StatusCode(), resp.Body)
				}
				pods := resp.JSON200.Data
				return render(cmd, pods, func(w io.Writer) error {
					return writePodTable(w, pods)
				})
			}

			params := &astroclient.GetApiV1ClustersClusterIdPodsParams{}
			if namespace != "" {
				params.Namespace = &namespace
			}
			resp, err := client.GetApiV1ClustersClusterIdPodsWithResponse(cmd.Context(), clusterID, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			pods := resp.JSON200.Data
			return render(cmd, pods, func(w io.Writer) error {
				return writePodTable(w, pods)
			})
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "restrict cluster-wide listing to a single namespace")
	return cmd
}

// ---------------------------------------------------------------------
// delete <cluster-id> <kind> <ns> <name>  (async — returns an operation)
// ---------------------------------------------------------------------

func newWorkloadsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <cluster-id> <kind> <namespace> <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a workload (async — returns a workload operation)",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to delete %s %s/%s on cluster %s.\nProceed? [y/N] ", args[1], args[2], args[3], args[0]); err != nil {
					return err
				}
				var ans string
				_, _ = fmt.Scanln(&ans)
				ans = strings.ToLower(strings.TrimSpace(ans))
				if ans != "y" && ans != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			resp, err := client.DeleteApiV1ClustersClusterIdWorkloadsKindNamespaceNameWithResponse(
				cmd.Context(), clusterID, args[1], args[2], args[3])
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			op := resp.JSON202.Data
			return render(cmd, op, func(w io.Writer) error {
				return writeOperationDetail(w, op)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ---------------------------------------------------------------------
// scale <cluster-id> <kind> <ns> <name> --replicas N
// ---------------------------------------------------------------------

func newWorkloadsScaleCmd() *cobra.Command {
	var replicas int32
	cmd := &cobra.Command{
		Use:   "scale <cluster-id> <kind> <namespace> <name>",
		Short: "Scale a workload to a desired replica count (async — returns an operation)",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("replicas") {
				return fmt.Errorf("--replicas is required")
			}
			if replicas < 0 {
				return fmt.Errorf("--replicas must be >= 0")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			body := astroclient.PatchApiV1ClustersClusterIdWorkloadsKindNamespaceNameScaleJSONRequestBody{
				Replicas: replicas,
			}
			resp, err := client.PatchApiV1ClustersClusterIdWorkloadsKindNamespaceNameScaleWithResponse(
				cmd.Context(), clusterID, args[1], args[2], args[3], body)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			op := resp.JSON202.Data
			return render(cmd, op, func(w io.Writer) error {
				return writeOperationDetail(w, op)
			})
		},
	}
	cmd.Flags().Int32Var(&replicas, "replicas", 0, "desired replica count (required)")
	return cmd
}

// ---------------------------------------------------------------------
// restart <cluster-id> <kind> <ns> <name>  (async — returns an operation)
// ---------------------------------------------------------------------

func newWorkloadsRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart <cluster-id> <kind> <namespace> <name>",
		Short: "Trigger a rolling restart of a workload (async — returns an operation)",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ClustersClusterIdWorkloadsKindNamespaceNameRestartWithResponse(
				cmd.Context(), clusterID, args[1], args[2], args[3])
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			op := resp.JSON202.Data
			return render(cmd, op, func(w io.Writer) error {
				return writeOperationDetail(w, op)
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// logs <cluster-id> <ns> <pod>
// ---------------------------------------------------------------------

func newWorkloadsLogsCmd() *cobra.Command {
	var container string
	var tailLines, sinceSeconds int
	cmd := &cobra.Command{
		Use:   "logs <cluster-id> <namespace> <pod>",
		Short: "Fetch logs for a pod",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1WorkloadsPodsClusterIdNamespacePodLogsParams{}
			if container != "" {
				params.Container = &container
			}
			if cmd.Flags().Changed("tail") {
				params.TailLines = &tailLines
			}
			if cmd.Flags().Changed("since") {
				params.SinceSeconds = &sinceSeconds
			}
			resp, err := client.GetApiV1WorkloadsPodsClusterIdNamespacePodLogsWithResponse(
				cmd.Context(), clusterID, args[1], args[2], params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			entries := resp.JSON200.Data
			return render(cmd, entries, func(w io.Writer) error {
				return writeLogEntries(w, entries)
			})
		},
	}
	cmd.Flags().StringVarP(&container, "container", "c", "", "target container")
	cmd.Flags().IntVar(&tailLines, "tail", 0, "number of trailing lines to return")
	cmd.Flags().IntVar(&sinceSeconds, "since", 0, "return logs newer than N seconds")
	return cmd
}

// ---------------------------------------------------------------------
// pod delete <cluster-id> <ns> <pod>
// ---------------------------------------------------------------------

func newWorkloadsPodCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pod",
		Short: "Act on individual pods",
	}
	cmd.AddCommand(newWorkloadsPodDeleteCmd())
	return cmd
}

func newWorkloadsPodDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <cluster-id> <namespace> <pod>",
		Aliases: []string{"rm"},
		Short:   "Delete (evict) a single pod",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			clusterID, err := parseWorkloadUUID(args[0], "cluster-id")
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to delete pod %s/%s on cluster %s.\nProceed? [y/N] ", args[1], args[2], args[0]); err != nil {
					return err
				}
				var ans string
				_, _ = fmt.Scanln(&ans)
				ans = strings.ToLower(strings.TrimSpace(ans))
				if ans != "y" && ans != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			resp, err := client.DeleteApiV1WorkloadsPodsClusterIdNamespacePodWithResponse(
				cmd.Context(), clusterID, args[1], args[2])
			if err != nil {
				return err
			}
			// This endpoint returns no typed success body (204-style).
			// Treat any 2xx as success; otherwise surface the error.
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return workloadSDKError(sc, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted pod %s/%s on cluster %s.\n", args[1], args[2], args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ---------------------------------------------------------------------
// operations list / get <id>, operation retry <id>
// ---------------------------------------------------------------------

func newWorkloadsOperationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "operations",
		Aliases: []string{"ops", "operation"},
		Short:   "Inspect and retry workload operations",
	}
	cmd.AddCommand(
		newWorkloadsOperationsListCmd(),
		newWorkloadsOperationsGetCmd(),
		newWorkloadsOperationsRetryCmd(),
	)
	return cmd
}

func newWorkloadsOperationsListCmd() *cobra.Command {
	var status, targetType, targetKey string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workload operations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1WorkloadsOperationsParams{}
			if status != "" {
				params.Status = &status
			}
			if targetType != "" {
				params.TargetType = &targetType
			}
			if targetKey != "" {
				params.TargetKey = &targetKey
			}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1WorkloadsOperationsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			ops := resp.JSON200.Data
			return render(cmd, ops, func(w io.Writer) error {
				return writeOperationTable(w, ops)
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&targetType, "target-type", "", "filter by target type")
	cmd.Flags().StringVar(&targetKey, "target-key", "", "filter by target key")
	cmd.Flags().IntVar(&limit, "limit", 0, "max items to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "number of items to skip")
	return cmd
}

func newWorkloadsOperationsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one workload operation (with events)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseWorkloadUUID(args[0], "operation id")
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1WorkloadsOperationsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			// renderSDK's generic table is fine for this rich struct; the
			// detailed events are best viewed with -o json/yaml.
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newWorkloadsOperationsRetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry a failed workload operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseWorkloadUUID(args[0], "operation id")
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1WorkloadsOperationsIdRetryWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			op := resp.JSON202.Data
			return render(cmd, op, func(w io.Writer) error {
				return writeOperationDetail(w, op)
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// controller-status
// ---------------------------------------------------------------------

func newWorkloadsControllerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "controller-status",
		Aliases: []string{"status"},
		Short:   "Show the workloads controller status (operation counts + reconciler state)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1WorkloadsControllerStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return workloadSDKError(resp.StatusCode(), resp.Body)
			}
			// The data shape is a small two-field object; the generic
			// renderer (with json/yaml fallbacks) is the right tool here.
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// parseUUID validates a path UUID argument up front so we return a clean
// CLI error rather than letting the server reject a malformed id.
func parseWorkloadUUID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid %s %q: must be a UUID", field, s)
	}
	return id, nil
}

// sdkError maps a non-success SDK response to a single clean error. The
// generated envelopes vary per endpoint, so we surface the HTTP status
// plus the trimmed raw body (which is the server's error JSON/text).
func workloadSDKError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	label := http.StatusText(status)
	if label == "" {
		label = fmt.Sprintf("status %d", status)
	}
	if msg == "" {
		return fmt.Errorf("request failed: %s (HTTP %d)", label, status)
	}
	return fmt.Errorf("request failed: %s (HTTP %d): %s", label, status, msg)
}

func writeWorkloadTable(w io.Writer, rows []astroclient.Workload) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no workloads)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "KIND\tNAMESPACE\tNAME\tREADY\tREPLICAS\tSTATUS\tAGE"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			strDeref(r.Kind), strDeref(r.Namespace), strDeref(r.Name),
			strDeref(r.Ready), intDeref(r.Replicas), strDeref(r.Status),
			strDeref(r.Age)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writePodTable(w io.Writer, rows []astroclient.Pod) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no pods)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAMESPACE\tNAME\tREADY\tPHASE\tRESTARTS\tNODE\tAGE"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			strDeref(r.Namespace), strDeref(r.Name), strDeref(r.Ready),
			strDeref(r.Phase), intDeref(r.Restarts), strDeref(r.Node),
			strDeref(r.Age)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeOperationTable(w io.Writer, rows []astroclient.WorkloadOperation) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no operations)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tTYPE\tTARGET\tSTATUS\tATTEMPTS\tERROR"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			uuidDeref(r.Id), strDeref(r.OperationType),
			operationTarget(r.TargetType, r.TargetKey), strDeref(r.Status),
			intDeref(r.AttemptCount), strDeref(r.ErrorMessage)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeOperationDetail(w io.Writer, op astroclient.WorkloadOperation) error {
	if _, err := fmt.Fprintf(w, "Operation: %s\n", uuidDeref(op.Id)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Type:      %s\n", strDeref(op.OperationType)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Target:    %s\n", operationTarget(op.TargetType, op.TargetKey)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Status:    %s\n", strDeref(op.Status)); err != nil {
		return err
	}
	if op.ErrorMessage != nil && *op.ErrorMessage != "" {
		if _, err := fmt.Fprintf(w, "Error:     %s\n", *op.ErrorMessage); err != nil {
			return err
		}
	}
	return nil
}

func writeLogEntries(w io.Writer, entries []astroclient.PodLogEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintln(w, "(no log entries)")
		return err
	}
	for _, e := range entries {
		ts := strDeref(e.Timestamp)
		msg := strDeref(e.Message)
		if ts != "" {
			if _, err := fmt.Fprintf(w, "%s %s\n", ts, msg); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(w, msg); err != nil {
			return err
		}
	}
	return nil
}

func operationTarget(targetType, targetKey *string) string {
	t, k := strDeref(targetType), strDeref(targetKey)
	switch {
	case t != "" && k != "":
		return t + "/" + k
	case k != "":
		return k
	case t != "":
		return t
	default:
		return "—"
	}
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func intDeref(i *int) string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("%d", *i)
}

func uuidDeref(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
