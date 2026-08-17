package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

func newDeliveryDeploymentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "deployment", Short: "Inspect and control Continuous Delivery deployments"}
	cmd.AddCommand(
		newDeliveryListCmd("deployment", "/api/v1/delivery/deployments/"),
		newDeliveryGetCmd("deployment", "/api/v1/delivery/deployments/%s/"),
		newDeliveryDeploymentActionCmd("reconcile", "Request reconciliation of one deployment"),
		newDeliveryDeploymentActionCmd("suspend", "Suspend one deployment"),
		newDeliveryDeploymentActionCmd("resume", "Resume one suspended deployment"),
		newDeliveryDeploymentEventsCmd(),
	)
	return cmd
}

func newDeliveryDeploymentActionCmd(action, short string) *cobra.Command {
	var project, body string
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			current, err := getDeliveryObject(cmd, fmt.Sprintf("/api/v1/delivery/deployments/%s/?project_id=%s", args[0], url.QueryEscape(projectID)))
			if err != nil {
				return err
			}
			headers := map[string]string{"If-Match": quotedEntityTag(deliveryDesiredGeneration(current))}
			path := fmt.Sprintf("/api/v1/delivery/deployments/%s/%s/?project_id=%s", args[0], action, url.QueryEscape(projectID))
			return runAPICommandWithHeaders(cmd, http.MethodPost, path, payload, headers, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "optional action JSON, @file, or - for stdin")
	return cmd
}

func newDeliveryDeploymentEventsCmd() *cobra.Command {
	var project string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "events <id>",
		Short: "List coalesced events for one deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			query := url.Values{
				"project_id": []string{projectID},
				"limit":      []string{strconv.Itoa(limit)},
				"offset":     []string{strconv.Itoa(offset)},
			}
			path := fmt.Sprintf("/api/v1/delivery/deployments/%s/events/?%s", args[0], query.Encode())
			return runAPICommand(cmd, http.MethodGet, path, nil, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum events to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "events to skip")
	return cmd
}

func deliveryDesiredGeneration(object map[string]any) int64 {
	switch value := object["desired_generation"].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return deliveryGeneration(object)
	}
}
