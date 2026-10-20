package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

const (
	systemCompatibilityPath = "/api/v1/delivery/system/compatibility/"
	systemRolloutsPath      = "/api/v1/delivery/system/rollouts/"
)

// newDeliverySystemRolloutCmd drives the post-upgrade promotion of the signed
// delivery-system release (Flux distribution + agent) that a server upgrade
// stages as a draft. See docs/upgrade-runbook.md.
func newDeliverySystemRolloutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system-rollout",
		Short: "Promote the staged delivery-system release (Flux + agent) to managed clusters",
		Long: `A server upgrade stages a new signed delivery-system release as a draft.
It reaches managed clusters only through a system rollout:

  astro delivery system-rollout start     # roll the pending release out
  astro delivery system-rollout approve   # approve the gated rollout
  astro delivery system-rollout status    # watch releases and progress`,
	}
	cmd.AddCommand(newSystemRolloutStartCmd(), newSystemRolloutApproveCmd(), newSystemRolloutStatusCmd())
	return cmd
}

// deliveryDefaultSystemRolloutStrategy rolls one cluster at a time and pauses
// on the first failure.
func deliveryDefaultSystemRolloutStrategy() map[string]any {
	return map[string]any{
		"type":              "rolling",
		"max_concurrent":    1,
		"max_unavailable":   map[string]any{"type": "count", "value": 1},
		"min_ready":         "0s",
		"progress_deadline": "30m",
		"failure_threshold": map[string]any{"type": "count", "value": 1},
		"on_failure":        "pause",
	}
}

func newSystemRolloutStartCmd() *cobra.Command {
	var release, strategy string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a system rollout of the pending (or given) system release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			releaseID := strings.TrimSpace(release)
			if releaseID == "" {
				compat, err := getDeliveryObject(cmd, systemCompatibilityPath)
				if err != nil {
					return err
				}
				pending, _ := compat["pending_release"].(map[string]any)
				releaseID, _ = pending["id"].(string)
				if releaseID == "" {
					return errors.New("no pending delivery-system release: managed clusters already run the newest release (pass --release to choose one)")
				}
			} else if _, err := requireUUIDFlag("release", releaseID); err != nil {
				return err
			}
			plan := deliveryDefaultSystemRolloutStrategy()
			if custom, err := optionalJSONObject(strategy); err != nil {
				return err
			} else if custom != nil {
				plan = custom
			}
			body := map[string]any{"release_id": releaseID, "strategy": plan}
			return runAPICommand(cmd, http.MethodPost, systemRolloutsPath, body, "")
		},
	}
	cmd.Flags().StringVar(&release, "release", "", "system release UUID (default: the pending release)")
	cmd.Flags().StringVar(&strategy, "strategy", "", "rollout strategy JSON, @file, or - for stdin (default: one cluster at a time, pause on failure)")
	return cmd
}

func newSystemRolloutApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve [rollout-id]",
		Short: "Approve the current (or given) system rollout",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := systemRolloutID(cmd, args)
			if err != nil {
				return err
			}
			current, err := getDeliveryObject(cmd, systemRolloutsPath+id+"/")
			if err != nil {
				return err
			}
			fence := deliveryFence(current)
			if fence < 1 {
				return fmt.Errorf("system rollout %s has no fencing generation", id)
			}
			headers := map[string]string{"If-Match": quotedEntityTag(fence)}
			return runAPICommandWithHeaders(cmd, http.MethodPost, systemRolloutsPath+id+"/approve/", map[string]any{}, headers, "")
		},
	}
}

func newSystemRolloutStatusCmd() *cobra.Command {
	var clusters bool
	cmd := &cobra.Command{
		Use:   "status [rollout-id]",
		Short: "Show the current/pending system releases and rollout, or one rollout",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !clusters {
				compat, err := getDeliveryObject(cmd, systemCompatibilityPath)
				if err != nil {
					return err
				}
				return renderSDK(cmd, map[string]any{
					"current_release": compat["current_release"], "pending_release": compat["pending_release"],
					"current_rollout": compat["current_rollout"],
				})
			}
			id, err := systemRolloutID(cmd, args)
			if err != nil {
				return err
			}
			if clusters {
				return runAPICommand(cmd, http.MethodGet, systemRolloutsPath+id+"/clusters/", nil, "")
			}
			return runAPICommand(cmd, http.MethodGet, systemRolloutsPath+id+"/", nil, "")
		},
	}
	cmd.Flags().BoolVar(&clusters, "clusters", false, "list the rollout's per-cluster assignments")
	return cmd
}

// systemRolloutID returns the explicit argument or the current rollout.
func systemRolloutID(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		if _, err := parseUUID("rollout-id", args[0]); err != nil {
			return "", err
		}
		return args[0], nil
	}
	compat, err := getDeliveryObject(cmd, systemCompatibilityPath)
	if err != nil {
		return "", err
	}
	rollout, _ := compat["current_rollout"].(map[string]any)
	id, _ := rollout["id"].(string)
	if id == "" {
		return "", errors.New("no current system rollout (run `astro delivery system-rollout start`)")
	}
	return id, nil
}
