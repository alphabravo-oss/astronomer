package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// newClusterAgentCmd exposes the retained cluster-agent lifecycle surface.
// Delivery rollout orchestration intentionally lives under `astro delivery`.
func newClusterAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-agent",
		Short: "Inspect and upgrade managed Cluster Agents",
		Long:  "Cluster Agents expose health, compatibility, diagnostics, and upgrade for Astronomer agents. Delivery rollouts live under astro delivery.",
	}
	cmd.AddCommand(
		newClusterAgentListCmd(),
		newClusterAgentGetCmd(),
		newClusterAgentDiagnosticsCmd(),
		newClusterAgentUpgradeCmd(),
	)
	return cmd
}

func newClusterAgentListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed cluster agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query := url.Values{}
			query.Set("limit", strconv.Itoa(limit))
			query.Set("offset", strconv.Itoa(offset))
			return runAPICommand(cmd, http.MethodGet, "/api/v1/cluster-agents/?"+query.Encode(), nil, "")
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum agents to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "agents to skip")
	return cmd
}

func newClusterAgentGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster-id>",
		Short: "Show one managed cluster agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseUUID("cluster-id", args[0]); err != nil {
				return err
			}
			return runAPICommand(cmd, http.MethodGet, fmt.Sprintf("/api/v1/cluster-agents/%s/", args[0]), nil, "")
		},
	}
}

func newClusterAgentDiagnosticsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnostics <cluster-id>",
		Short: "Collect diagnostics for one managed cluster agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseUUID("cluster-id", args[0]); err != nil {
				return err
			}
			return runAPICommand(cmd, http.MethodGet, fmt.Sprintf("/api/v1/cluster-agents/%s/diagnostics/", args[0]), nil, "")
		},
	}
}

func newClusterAgentUpgradeCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "upgrade <cluster-id>",
		Short: "Queue a managed cluster agent upgrade",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseUUID("cluster-id", args[0]); err != nil {
				return err
			}
			payload, err := readJSONInput(body)
			if err != nil {
				return err
			}
			return runAPICommand(cmd, http.MethodPost, fmt.Sprintf("/api/v1/cluster-agents/%s/upgrade/", args[0]), payload, "")
		},
	}
	cmd.Flags().StringVar(&body, "data", "", "upgrade request JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}
