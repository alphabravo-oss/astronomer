// k8s passthrough subcommand: `astro k8s <cluster-id> <api-path>`.
//
// Thin wrapper around the management plane's /api/v1/clusters/{id}/k8s/*
// passthrough proxy. Lets operators run k8s reads directly through the
// platform's tunnel without distributing a kubeconfig — useful for
// automation that already has an `astro login` token but doesn't want
// to manage per-cluster kubeconfig files.
//
// We deliberately don't try to mimic kubectl's flag surface (-n,
// -o yaml, --context, …). The CLI just hands the path through to the
// proxy and prints the body. Operators who want kubectl ergonomics
// should use `astro cluster shell` (interactive kubectl in-cluster)
// or generate a real kubeconfig.

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newK8sCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k8s <cluster-id> <api-path>",
		Short: "Proxy a Kubernetes API request through the cluster's agent tunnel",
		Long: `Hands the path off to the management plane's /api/v1/clusters/{id}/k8s/*
endpoint, which forwards it to the managed cluster's apiserver via
the agent WebSocket. The response body is printed verbatim (JSON).

Examples:

  astro k8s <cluster-id> api/v1/namespaces
  astro k8s <cluster-id> apis/apps/v1/namespaces/default/deployments
  astro k8s <cluster-id> apis/aquasecurity.github.io/v1alpha1/vulnerabilityreports

The API path is whatever kubectl would put after "kubectl get --raw=".
Leading slashes are stripped automatically.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(cmd)
			if err != nil {
				return err
			}
			clusterID := args[0]
			apiPath := strings.TrimLeft(strings.Join(args[1:], "/"), "/")
			body, err := client.GetRaw(cmd.Context(),
				"/api/v1/clusters/"+clusterID+"/k8s/"+apiPath)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			if err == nil && len(body) > 0 && body[len(body)-1] != '\n' {
				_, err = fmt.Fprintln(cmd.OutOrStdout())
			}
			return err
		},
	}
	return cmd
}
