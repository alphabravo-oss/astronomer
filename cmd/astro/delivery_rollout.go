package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newDeliveryRolloutCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rollout", Short: "Control Continuous Delivery rollouts"}
	cmd.AddCommand(
		newDeliveryListCmd("rollout", "/api/v1/delivery/rollouts/"),
		newDeliveryGetCmd("rollout", "/api/v1/delivery/rollouts/%s/"),
		newDeliveryRolloutStartCmd(),
		newDeliveryRolloutActionCmd("pause", "Pause a progressing rollout"),
		newDeliveryRolloutActionCmd("resume", "Resume a paused rollout"),
		newDeliveryRolloutActionCmd("approve", "Approve a gated rollout cohort"),
		newDeliveryRolloutActionCmd("abort", "Abort a rollout"),
		newDeliveryRolloutActionCmd("retry", "Retry failed rollout clusters"),
		newDeliveryRolloutActionCmd("rollback", "Roll back to the frozen previous revision"),
		newDeliveryRolloutWatchCmd(),
	)
	return cmd
}

func newDeliveryRolloutStartCmd() *cobra.Command {
	var project, target, body string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Freeze preview and launch a rollout for one target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			targetID, err := requireUUIDFlag("target", target)
			if err != nil {
				return err
			}
			payload, err := optionalJSONInput(body)
			if err != nil {
				return err
			}
			if payload == nil {
				payload = map[string]any{"project_id": projectID}
			}
			current, err := getDeliveryObject(cmd, fmt.Sprintf("/api/v1/delivery/targets/%s/?project_id=%s", targetID, url.QueryEscape(projectID)))
			if err != nil {
				return err
			}
			generation := deliveryGeneration(current)
			headers := map[string]string{"If-Match": quotedEntityTag(generation)}
			path := fmt.Sprintf("/api/v1/delivery/targets/%s/rollouts/?project_id=%s", targetID, url.QueryEscape(projectID))
			return runAPICommandWithHeaders(cmd, http.MethodPost, path, payload, headers, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&target, "target", "", "target UUID")
	cmd.Flags().StringVar(&body, "data", "", "optional start JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func newDeliveryRolloutActionCmd(action, short string) *cobra.Command {
	var project, body string
	var yes bool
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if action == "abort" || action == "rollback" {
				if err := requireDestructiveConfirmation(cmd, yes, action+" rollout "+args[0]); err != nil {
					return err
				}
			}
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			payload, err := optionalJSONInput(body)
			if err != nil {
				return err
			}
			if payload == nil {
				payload = map[string]any{"project_id": projectID}
			}
			current, err := getDeliveryObject(cmd, fmt.Sprintf("/api/v1/delivery/rollouts/%s/?project_id=%s", args[0], url.QueryEscape(projectID)))
			if err != nil {
				return err
			}
			headers := map[string]string{"If-Match": quotedEntityTag(deliveryFence(current))}
			path := fmt.Sprintf("/api/v1/delivery/rollouts/%s/%s/?project_id=%s", args[0], action, url.QueryEscape(projectID))
			return runAPICommandWithHeaders(cmd, http.MethodPost, path, payload, headers, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "optional action JSON, @file, or - for stdin")
	if action == "abort" || action == "rollback" {
		cmd.Flags().BoolVar(&yes, "yes", false, "confirm a destructive rollout action without a prompt")
	}
	return cmd
}

func newDeliveryRolloutWatchCmd() *cobra.Command {
	var project string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch <id>",
		Short: "Poll one rollout until it reaches a terminal state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/delivery/rollouts/%s/?project_id=%s", args[0], url.QueryEscape(projectID))
			for {
				current, err := getDeliveryObject(cmd, path)
				if err != nil {
					return err
				}
				if err := renderSDK(cmd, current); err != nil {
					return err
				}
				state, _ := current["state"].(string)
				if deliveryRolloutTerminal(state) {
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(interval):
				}
			}
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "poll interval")
	return cmd
}

func deliveryRolloutTerminal(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "succeeded", "failed", "aborted", "rejected", "rolled_back", "rollback_failed":
		return true
	default:
		return false
	}
}

func deliveryFence(object map[string]any) int64 {
	switch value := object["fencing_generation"].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func getDeliveryObject(cmd *cobra.Command, path string) (map[string]any, error) {
	client, _, err := authedClient(cmd)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := client.Do(cmd.Context(), http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if data, ok := response["data"].(map[string]any); ok {
		if nested, ok := data["target"].(map[string]any); ok {
			return nested, nil
		}
		if nested, ok := data["rollout"].(map[string]any); ok {
			return nested, nil
		}
		if nested, ok := data["deployment"].(map[string]any); ok {
			return nested, nil
		}
		return data, nil
	}
	return response, nil
}

func optionalJSONInput(value string) (any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return readJSONInput(value)
}
