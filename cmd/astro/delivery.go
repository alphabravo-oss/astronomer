package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newDeliveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Manage Continuous Delivery sources, bundles, targets, rollouts, and deployments",
		Long: `Continuous Delivery is Astronomer's Flux-native delivery engine.

Register Git, OCI, and Helm sources, version bundles, preview placement,
and control rollouts. Cluster Agents are a separate command: astro cluster-agent.`,
	}
	cmd.AddCommand(
		newDeliverySourceCmd(),
		newDeliveryBundleCmd(),
		newDeliveryTargetCmd(),
		newDeliveryRolloutCmd(),
		newDeliveryDeploymentCmd(),
	)
	return cmd
}

func newDeliverySourceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "source", Short: "Manage immutable Continuous Delivery sources"}
	cmd.AddCommand(
		newDeliveryListCmd("source", "/api/v1/delivery/sources/"),
		newDeliveryGetCmd("source", "/api/v1/delivery/sources/%s/"),
		newDeliveryMutationCmd("create", "Create a delivery source", http.MethodPost, "/api/v1/delivery/sources/", false),
		newDeliveryMutationCmd("update <id>", "Update credential-free source metadata", http.MethodPatch, "/api/v1/delivery/sources/%s/", true),
		newDeliveryDeleteCmd("source", "/api/v1/delivery/sources/%s/", false),
		newDeliveryMutationCmd("verify <id>", "Queue source resolution and trust verification", http.MethodPost, "/api/v1/delivery/sources/%s/verify/", true),
		newDeliveryMutationCmd("rotate-credential <id>", "Rotate a source credential", http.MethodPost, "/api/v1/delivery/sources/%s/rotate-credential/", true),
	)
	return cmd
}

func newDeliveryBundleCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "bundle", Short: "Manage versioned Continuous Delivery bundles"}
	cmd.AddCommand(
		newDeliveryListCmd("bundle", "/api/v1/delivery/bundles/"),
		newDeliveryGetCmd("bundle", "/api/v1/delivery/bundles/%s/"),
		newDeliveryMutationCmd("create", "Create a delivery bundle", http.MethodPost, "/api/v1/delivery/bundles/", false),
		newDeliveryDeleteCmd("bundle", "/api/v1/delivery/bundles/%s/", false),
		newDeliveryBundleVersionListCmd(),
		newDeliveryMutationCmd("version-create <bundle-id>", "Create an immutable bundle version", http.MethodPost, "/api/v1/delivery/bundles/%s/versions/", true),
		newDeliveryBundleVersionGetCmd(),
	)
	return cmd
}

func newDeliveryListCmd(noun, path string) *cobra.Command {
	var project string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List delivery " + noun + "s",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			query := url.Values{"project_id": []string{projectID}, "limit": []string{strconv.Itoa(limit)}, "offset": []string{strconv.Itoa(offset)}}
			return runAPICommand(cmd, http.MethodGet, path+"?"+query.Encode(), nil, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum records to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "records to skip")
	return cmd
}

func newDeliveryGetCmd(noun, path string) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one delivery " + noun,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			return runAPICommand(cmd, http.MethodGet, fmt.Sprintf(path, args[0])+"?project_id="+url.QueryEscape(projectID), nil, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	return cmd
}

func newDeliveryMutationCmd(use, short, method, path string, hasID bool) *cobra.Command {
	var project, body string
	argsValidator := cobra.NoArgs
	if hasID {
		argsValidator = cobra.ExactArgs(1)
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  argsValidator,
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			requestPath := path
			if hasID {
				if _, err := parseUUID("id", args[0]); err != nil {
					return err
				}
				requestPath = fmt.Sprintf(path, args[0])
			}
			payload, err := readJSONInput(body)
			if err != nil {
				return err
			}
			return runAPICommand(cmd, method, requestPath+"?project_id="+url.QueryEscape(projectID), payload, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "request JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

func newDeliveryDeleteCmd(noun, path string, requireMatch bool) *cobra.Command {
	var project string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a delivery " + noun,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDestructiveConfirmation(cmd, yes, "delete delivery "+noun+" "+args[0]); err != nil {
				return err
			}
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			headers := map[string]string{}
			if requireMatch {
				current, err := getDeliveryObject(cmd, fmt.Sprintf(path, args[0])+"?project_id="+url.QueryEscape(projectID))
				if err != nil {
					return err
				}
				request, err := deliveryTargetDeleteRequest(projectID, args[0], current)
				if err != nil {
					return err
				}
				return runAPICommandWithHeaders(cmd, request.Method, request.Path, request.Body, request.Headers, "")
			}
			return runAPICommandWithHeaders(cmd, http.MethodDelete, fmt.Sprintf(path, args[0])+"?project_id="+url.QueryEscape(projectID), nil, headers, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a destructive delete without a prompt")
	return cmd
}

func newDeliveryBundleVersionListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "version-list <bundle-id>",
		Short: "List immutable bundle versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("bundle-id", args[0]); err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/delivery/bundles/%s/versions/?project_id=%s", args[0], url.QueryEscape(projectID))
			return runAPICommand(cmd, http.MethodGet, path, nil, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	return cmd
}

func newDeliveryBundleVersionGetCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "version-get <bundle-id> <version-id>",
		Short: "Show one immutable bundle version",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			for i, label := range []string{"bundle-id", "version-id"} {
				if _, err := parseUUID(label, args[i]); err != nil {
					return err
				}
			}
			path := fmt.Sprintf("/api/v1/delivery/bundles/%s/versions/%s/?project_id=%s", args[0], args[1], url.QueryEscape(projectID))
			return runAPICommand(cmd, http.MethodGet, path, nil, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	return cmd
}

func addDeliveryProjectFlag(cmd *cobra.Command, destination *string) {
	cmd.Flags().StringVar(destination, "project", "", "project UUID")
	_ = cmd.MarkFlagRequired("project")
}

type deliveryHTTPRequest struct {
	Method  string
	Path    string
	Headers map[string]string
	Body    any
}

func deliveryTargetDeleteRequest(projectID, targetID string, target map[string]any) (deliveryHTTPRequest, error) {
	version := deliveryResourceVersion(target)
	if version < 1 {
		return deliveryHTTPRequest{}, fmt.Errorf("target delete requires a positive resource_version for If-Match")
	}
	return deliveryHTTPRequest{
		Method:  http.MethodDelete,
		Path:    fmt.Sprintf("/api/v1/delivery/targets/%s/?project_id=%s", targetID, url.QueryEscape(projectID)),
		Headers: map[string]string{"If-Match": quotedEntityTag(version)},
	}, nil
}

func quotedEntityTag(version int64) string {
	return fmt.Sprintf("\"%d\"", version)
}

func requireUUIDFlag(name, value string) (string, error) {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return "", fmt.Errorf("--%s must be a UUID: %w", name, err)
	}
	return strings.TrimSpace(value), nil
}

func readJSONInput(value string) (json.RawMessage, error) {
	value = strings.TrimSpace(value)
	var raw []byte
	var err error
	switch {
	case value == "-" || value == "@-":
		raw, err = io.ReadAll(os.Stdin)
	case strings.HasPrefix(value, "@"):
		raw, err = os.ReadFile(strings.TrimPrefix(value, "@"))
	default:
		raw = []byte(value)
	}
	if err != nil {
		return nil, fmt.Errorf("read request data: %w", err)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("request data must be valid JSON")
	}
	return json.RawMessage(raw), nil
}

func requireDestructiveConfirmation(cmd *cobra.Command, yes bool, action string) error {
	if yes || isInteractiveDelivery(cmd) {
		return nil
	}
	return fmt.Errorf("%s requires --yes in non-interactive mode", action)
}

func isInteractiveDelivery(cmd *cobra.Command) bool {
	if file, ok := cmd.InOrStdin().(*os.File); ok {
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	return false
}

func runAPICommand(cmd *cobra.Command, method, path string, body any, selectField string) error {
	return runAPICommandWithHeaders(cmd, method, path, body, nil, selectField)
}

func runAPICommandWithHeaders(cmd *cobra.Command, method, path string, body any, extra map[string]string, selectField string) error {
	client, _, err := authedClient(cmd)
	if err != nil {
		return err
	}
	var response map[string]any
	var out any = &response
	if method == http.MethodDelete {
		out = nil
	}
	headers := map[string]string{}
	if method != http.MethodGet {
		headers["Idempotency-Key"] = uuid.NewString()
	}
	for key, value := range extra {
		headers[key] = value
	}
	if err := client.DoWithHeaders(cmd.Context(), method, path, body, headers, out); err != nil {
		return err
	}
	if out == nil {
		return renderSDK(cmd, map[string]any{"status": "deleted"})
	}
	data := any(response)
	if value, ok := response["data"]; ok {
		data = value
	}
	if selectField != "" {
		if object, ok := data.(map[string]any); ok {
			if value, found := object[selectField]; found {
				data = value
			}
		}
	}
	return renderSDK(cmd, data)
}
