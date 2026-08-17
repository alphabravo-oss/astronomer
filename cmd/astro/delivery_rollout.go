package main

import (
	"encoding/json"
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
			userPayload, err := optionalJSONObject(body)
			if err != nil {
				return err
			}
			current, err := getDeliveryObject(cmd, fmt.Sprintf("/api/v1/delivery/targets/%s/?project_id=%s", targetID, url.QueryEscape(projectID)))
			if err != nil {
				return err
			}
			preview, err := postDeliveryPreview(cmd, projectID, targetID)
			if err != nil {
				return err
			}
			request, err := deliveryRolloutStartRequest(projectID, targetID, current, preview, userPayload)
			if err != nil {
				return err
			}
			return runAPICommandWithHeaders(cmd, request.Method, request.Path, request.Body, request.Headers, "")
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

func deliveryDefaultRolloutStrategy() map[string]any {
	return map[string]any{
		"type":                        "all_at_once",
		"max_concurrent":              10,
		"max_unavailable":             map[string]any{"type": "count", "value": 0},
		"min_ready":                   "0s",
		"progress_deadline":           "30m",
		"failure_threshold":           map[string]any{"type": "count", "value": 1},
		"on_failure":                  "pause",
		"respect_maintenance_windows": true,
	}
}

func deliveryRolloutStartRequest(projectID, targetID string, target, preview, userPayload map[string]any) (deliveryHTTPRequest, error) {
	generation := deliveryGeneration(target)
	if generation < 1 {
		return deliveryHTTPRequest{}, fmt.Errorf("rollout start requires a positive target generation for If-Match")
	}
	digest := deliveryPreviewDigest(preview)
	if digest == "" {
		return deliveryHTTPRequest{}, fmt.Errorf("rollout start requires a frozen sha256 preview_digest")
	}
	body := map[string]any{
		"project_id":           projectID,
		"preview_digest":       digest,
		"confirm_all_clusters": deliveryBool(preview["requires_all_confirmation"]),
		"strategy":             deliveryDefaultRolloutStrategy(),
	}
	for key, value := range userPayload {
		if key == "preview_digest" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "strategy" && value == nil {
			continue
		}
		body[key] = value
	}
	if strings.TrimSpace(fmt.Sprint(body["preview_digest"])) == "" {
		body["preview_digest"] = digest
	}
	return deliveryHTTPRequest{
		Method:  http.MethodPost,
		Path:    fmt.Sprintf("/api/v1/delivery/targets/%s/rollouts/?project_id=%s", targetID, url.QueryEscape(projectID)),
		Headers: map[string]string{"If-Match": quotedEntityTag(generation)},
		Body:    body,
	}, nil
}

func deliveryPreviewDigest(preview map[string]any) string {
	value := strings.TrimSpace(fmt.Sprint(preview["preview_digest"]))
	if value == "" || value == "<nil>" {
		return ""
	}
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return ""
	}
	return value
}

func deliveryBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
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
	return unwrapDeliveryData(response), nil
}

func postDeliveryPreview(cmd *cobra.Command, projectID, targetID string) (map[string]any, error) {
	client, _, err := authedClient(cmd)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/delivery/targets/%s/preview/?project_id=%s", targetID, url.QueryEscape(projectID))
	var response map[string]any
	if err := client.Do(cmd.Context(), http.MethodPost, path, map[string]any{"project_id": projectID}, &response); err != nil {
		return nil, err
	}
	return unwrapDeliveryData(response), nil
}

func unwrapDeliveryData(response map[string]any) map[string]any {
	data, ok := response["data"].(map[string]any)
	if !ok {
		return response
	}
	for _, key := range []string{"target", "rollout", "deployment"} {
		if nested, ok := data[key].(map[string]any); ok {
			return nested
		}
	}
	return data
}

func optionalJSONInput(value string) (any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return readJSONInput(value)
}

func optionalJSONObject(value string) (map[string]any, error) {
	raw, err := optionalJSONInput(value)
	if err != nil || raw == nil {
		return nil, err
	}
	message, ok := raw.(json.RawMessage)
	if !ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("start data must be one JSON object")
		}
		message = encoded
	}
	var object map[string]any
	if err := json.Unmarshal(message, &object); err != nil || object == nil {
		return nil, fmt.Errorf("start data must be one JSON object")
	}
	return object, nil
}
