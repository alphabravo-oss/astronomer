// Cluster subcommands: list / get / create / delete / manifest / self-test / shell.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type clusterRow struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	DisplayName       string  `json:"display_name"`
	Status            string  `json:"status"`
	Provider          string  `json:"provider"`
	Environment       string  `json:"environment"`
	NodeCount         int     `json:"node_count"`
	PodCount          int     `json:"pod_count"`
	KubernetesVersion string  `json:"kubernetes_version"`
	AgentVersion      string  `json:"agent_version"`
	IsLocal           bool    `json:"is_local"`
	LastHeartbeat     *string `json:"last_heartbeat"`
	RegistrationPhase string  `json:"registration_phase"`
}

type clusterList struct {
	Data  []clusterRow `json:"data"`
	Count int          `json:"count"`
}

type singleCluster struct {
	Data clusterRow `json:"data"`
}

type agentSelfTestEnvelope struct {
	Data agentSelfTestResult `json:"data"`
}

type agentSelfTestResult struct {
	GeneratedAt     string               `json:"generated_at"`
	ClusterID       string               `json:"cluster_id"`
	ClusterName     string               `json:"cluster_name"`
	Status          string               `json:"status"`
	Checks          []agentSelfTestCheck `json:"checks"`
	Recommendations []string             `json:"recommendations"`
}

type agentSelfTestCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster",
		Aliases: []string{"clusters"},
		Short:   "Manage clusters",
	}
	cmd.AddCommand(
		newClusterListCmd(),
		newClusterGetCmd(),
		newClusterCreateCmd(),
		newClusterDeleteCmd(),
		newClusterManifestCmd(),
		newClusterSelfTestCmd(),
		newClusterShellCmd(),
	)
	return cmd
}

func newClusterListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all clusters",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			var lst clusterList
			if err := client.Do(cmd.Context(), "GET", "/api/v1/clusters/", nil, &lst); err != nil {
				return err
			}
			return render(cmd, lst.Data, func(w io.Writer) error {
				return writeClusterTable(w, lst.Data)
			})
		},
	}
	return cmd
}

func newClusterGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one cluster's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			var out singleCluster
			if err := client.Do(cmd.Context(), "GET", "/api/v1/clusters/"+args[0]+"/", nil, &out); err != nil {
				return err
			}
			return render(cmd, out.Data, func(w io.Writer) error {
				return writeClusterTable(w, []clusterRow{out.Data})
			})
		},
	}
	return cmd
}

func newClusterCreateCmd() *cobra.Command {
	var displayName, description, environment, region, provider, distribution string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Register a new cluster (returns the cluster row; use `astro cluster manifest <id>` next)",
		Long: `create posts to /api/v1/clusters/ and prints the new cluster's
ID + registration phase. The next steps for the operator:

  astro cluster manifest <id> | kubectl --context=<target> apply --server-side --field-manager=astronomer-bootstrap -f -

That installs the agent into the target cluster, which then connects
back. Run "astro cluster get <id>" to watch the registration phase
advance.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{
				"name": args[0],
			}
			if displayName != "" {
				body["display_name"] = displayName
			}
			if description != "" {
				body["description"] = description
			}
			if environment != "" {
				body["environment"] = environment
			}
			if region != "" {
				body["region"] = region
			}
			if provider != "" {
				body["provider"] = provider
			}
			if distribution != "" {
				body["distribution"] = distribution
			}
			var out singleCluster
			if err := client.Do(cmd.Context(), "POST", "/api/v1/clusters/", body, &out); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Created cluster %s (%s)\n", out.Data.Name, out.Data.ID); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Next: astro cluster manifest %s | kubectl apply --server-side --field-manager=astronomer-bootstrap -f -\n", out.Data.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&displayName, "display-name", "", "human-readable name")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringVar(&environment, "environment", "", "production|staging|development")
	cmd.Flags().StringVar(&region, "region", "", "free-form region tag")
	cmd.Flags().StringVar(&provider, "provider", "", "eks|gke|aks|k3d|kind|other")
	cmd.Flags().StringVar(&distribution, "distribution", "", "vanilla|k3s|openshift|...")
	return cmd
}

func newClusterDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm", "decommission"},
		Short:   "Decommission a cluster (async — agent token revoked, audit archived, row tombstoned)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to decommission cluster %s. The underlying Kubernetes cluster is NOT destroyed.\nProceed? [y/N] ", args[0]); err != nil {
					return err
				}
				var resp string
				_, _ = fmt.Scanln(&resp)
				resp = strings.ToLower(strings.TrimSpace(resp))
				if resp != "y" && resp != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			// Server-side trailing-slash middleware accepts either form
			// since this session; we send the canonical trailing-slash
			// form to match the OpenAPI spec we ship.
			if err := client.Do(cmd.Context(), "DELETE", "/api/v1/clusters/"+args[0]+"/", nil, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Decommission queued for %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newClusterManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest <id>",
		Short: "Download the agent install YAML for a cluster",
		Long: `Prints the cluster-specific agent install manifest to stdout.
Pipe directly into kubectl:

  astro cluster manifest <id> | kubectl apply --server-side --field-manager=astronomer-bootstrap -f -

Each call mints a fresh short-lived registration token; safe to re-run.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			body, err := client.GetRaw(cmd.Context(), "/api/v1/clusters/"+args[0]+"/manifest/")
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
	return cmd
}

func newClusterSelfTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-test <id>",
		Short: "Run agent health checks for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			var out agentSelfTestEnvelope
			if err := client.Do(cmd.Context(), "POST", "/api/v1/agents/fleet/"+args[0]+"/self-test/", map[string]any{}, &out); err != nil {
				return err
			}
			return render(cmd, out.Data, func(w io.Writer) error {
				return writeAgentSelfTest(w, out.Data)
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

func writeClusterTable(w io.Writer, rows []clusterRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tPHASE\tPROVIDER\tNODES\tPODS\tK8S\tHEARTBEAT"); err != nil {
		return err
	}
	for _, r := range rows {
		hb := "—"
		if r.LastHeartbeat != nil {
			if t, err := time.Parse(time.RFC3339, *r.LastHeartbeat); err == nil {
				hb = humanize(time.Since(t))
			}
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			truncate(r.ID, 8), r.Name, r.Status, defaultStr(r.RegistrationPhase, "—"),
			defaultStr(r.Provider, "—"), r.NodeCount, r.PodCount,
			defaultStr(r.KubernetesVersion, "—"), hb); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeAgentSelfTest(w io.Writer, result agentSelfTestResult) error {
	name := defaultStr(result.ClusterName, result.ClusterID)
	if _, err := fmt.Fprintf(w, "Cluster:   %s\n", name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Self-test: %s\n", strings.ToUpper(defaultStr(result.Status, "unknown"))); err != nil {
		return err
	}
	if result.GeneratedAt != "" {
		if _, err := fmt.Fprintf(w, "Generated: %s\n", result.GeneratedAt); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "CHECK\tSTATUS\tMESSAGE"); err != nil {
		return err
	}
	for _, check := range result.Checks {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", check.Name, strings.ToUpper(defaultStr(check.Status, "unknown")), check.Message); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(result.Recommendations) > 0 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Recommendations:"); err != nil {
			return err
		}
		for _, recommendation := range result.Recommendations {
			if _, err := fmt.Fprintf(w, "- %s\n", recommendation); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func defaultStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func humanize(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < 2*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ---------------------------------------------------------------------
// Shell — opens a session, attaches via the existing /ws/.../shell/...
// endpoint. The WS upgrade requires a JWT cookie/bearer that this
// process has; we just open the session and then hand the operator the
// URL since making an interactive WS terminal client is a separate
// effort. Phase 1 of the CLI ships "open + print URL"; phase 2 will
// upgrade to a real interactive terminal.
// ---------------------------------------------------------------------

type shellSession struct {
	Data struct {
		ID           string `json:"id"`
		ClusterID    string `json:"cluster_id"`
		Status       string `json:"status"`
		PodName      string `json:"pod_name"`
		PodNamespace string `json:"pod_namespace"`
		Container    string `json:"container"`
		StartedAt    string `json:"started_at"`
		ExpiresAt    string `json:"expires_at"`
	} `json:"data"`
}

func newClusterShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell <id>",
		Short: "Open a kubectl shell session on a cluster",
		Long: `Creates an ephemeral kubectl shell pod on the cluster and
prints the WebSocket URL the operator can attach to. The session
auto-reaps after 4 hours or 30 minutes idle.

Today this command opens the session and prints the WS URL — point a
WS-aware terminal client at it, or use the dashboard's built-in
xterm. Interactive in-terminal attachment will land in a follow-up.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			var sess shellSession
			if err := client.Do(ctx, "POST", "/api/v1/clusters/"+args[0]+"/shell/sessions/", map[string]any{}, &sess); err != nil {
				return err
			}
			wsBase := strings.TrimSuffix(strings.Replace(strings.Replace(cfg.ServerURL, "https://", "wss://", 1), "http://", "ws://", 1), "/")
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Session: %s\n", sess.Data.ID); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pod:     %s/%s (container: %s)\n", sess.Data.PodNamespace, sess.Data.PodName, sess.Data.Container); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", sess.Data.ExpiresAt); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "WS URL:  %s/api/v1/ws/clusters/%s/shell/sessions/%s/\n", wsBase, args[0], sess.Data.ID); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "         (use Bearer %s in the Authorization header)\n", "<your token from ~/.config/astronomer/config.yaml>")
			return err
		},
	}
	return cmd
}

// silence "imported and not used" if we end up not using these
var _ = os.Stdout
