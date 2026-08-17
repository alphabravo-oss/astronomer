package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newDeliveryTargetCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "target", Short: "Manage Continuous Delivery targets and placement"}
	cmd.AddCommand(
		newDeliveryListCmd("target", "/api/v1/delivery/targets/"),
		newDeliveryGetCmd("target", "/api/v1/delivery/targets/%s/"),
		newDeliveryTargetApplyCmd(),
		newDeliveryDeleteCmd("target", "/api/v1/delivery/targets/%s/"),
		newDeliveryTargetPreviewCmd(),
	)
	return cmd
}

func newDeliveryTargetPreviewCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "preview <id>",
		Short: "Preview exact placement without launching a rollout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/delivery/targets/%s/preview/?project_id=%s", args[0], url.QueryEscape(projectID))
			return runAPICommand(cmd, http.MethodPost, path, map[string]any{"project_id": projectID}, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	return cmd
}

func newDeliveryTargetApplyCmd() *cobra.Command {
	var file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Idempotently create or update a target from a YAML/JSON document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readDeliveryFile(file)
			if err != nil {
				return err
			}
			document, err := parseDeliveryTargetDocument(raw)
			if err != nil {
				return err
			}
			existing, found, err := lookupDeliveryTargetByName(cmd, document.ProjectID, document.Name)
			if err != nil {
				return err
			}
			if dryRun {
				action := "create"
				if found {
					action = "update"
				}
				return renderSDK(cmd, map[string]any{"dry_run": true, "action": action, "target": document.Body})
			}
			if !found {
				return runAPICommand(cmd, http.MethodPost, "/api/v1/delivery/targets/?project_id="+url.QueryEscape(document.ProjectID), document.Body, "")
			}
			id, _ := existing["id"].(string)
			version := deliveryResourceVersion(existing)
			patch := deliveryTargetPatchBody(document.Body)
			return runAPICommandWithHeaders(cmd, http.MethodPatch, fmt.Sprintf("/api/v1/delivery/targets/%s/?project_id=%s", id, url.QueryEscape(document.ProjectID)), patch, map[string]string{"If-Match": quotedEntityTag(version)}, "")
		},
	}
	cmd.Flags().StringVarP(&file, "filename", "f", "", "target document path, or - for stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the create/update decision without mutating")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

type deliveryTargetDocument struct {
	ProjectID string
	Name      string
	Body      map[string]any
}

func parseDeliveryTargetDocument(raw []byte) (deliveryTargetDocument, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return deliveryTargetDocument{}, fmt.Errorf("target document is empty")
	}
	var decoded any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return deliveryTargetDocument{}, fmt.Errorf("target document must be valid YAML or JSON: %w", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return deliveryTargetDocument{}, fmt.Errorf("target document must be one object")
	}
	if kind, _ := object["kind"].(string); strings.EqualFold(kind, "DeliveryTarget") {
		if spec, ok := object["spec"].(map[string]any); ok {
			if _, exists := spec["name"]; !exists {
				if metadata, ok := object["metadata"].(map[string]any); ok {
					spec["name"] = metadata["name"]
				}
			}
			if _, exists := spec["project_id"]; !exists {
				if metadata, ok := object["metadata"].(map[string]any); ok {
					spec["project_id"] = firstNonEmptyString(metadata["project_id"], metadata["namespace"])
				}
			}
			object = spec
		}
	}
	name := strings.TrimSpace(fmt.Sprint(object["name"]))
	if name == "" || name == "<nil>" {
		return deliveryTargetDocument{}, fmt.Errorf("target document requires name")
	}
	projectID := strings.TrimSpace(fmt.Sprint(object["project_id"]))
	if _, err := uuid.Parse(projectID); err != nil {
		return deliveryTargetDocument{}, fmt.Errorf("target document requires project_id UUID")
	}
	object["name"] = name
	object["project_id"] = projectID
	return deliveryTargetDocument{ProjectID: projectID, Name: name, Body: object}, nil
}

func lookupDeliveryTargetByName(cmd *cobra.Command, projectID, name string) (map[string]any, bool, error) {
	client, _, err := authedClient(cmd)
	if err != nil {
		return nil, false, err
	}
	query := url.Values{"project_id": []string{projectID}, "name": []string{name}, "limit": []string{"1"}}
	var response map[string]any
	if err := client.Do(cmd.Context(), http.MethodGet, "/api/v1/delivery/targets/?"+query.Encode(), nil, &response); err != nil {
		return nil, false, err
	}
	data := response["data"]
	switch typed := data.(type) {
	case []any:
		if len(typed) == 0 {
			return nil, false, nil
		}
		item, ok := typed[0].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("target list item is invalid")
		}
		return item, true, nil
	case map[string]any:
		return typed, true, nil
	default:
		return nil, false, nil
	}
}

func deliveryTargetPatchBody(body map[string]any) map[string]any {
	patch := map[string]any{}
	for _, key := range []string{
		"project_id", "description", "bundle_version_id", "placement",
		"rollout_policy", "reconciliation_policy", "maintenance_window_policy", "suspended",
	} {
		if value, ok := body[key]; ok {
			patch[key] = value
		}
	}
	return patch
}

func deliveryResourceVersion(object map[string]any) int64 {
	switch value := object["resource_version"].(type) {
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func deliveryGeneration(object map[string]any) int64 {
	switch value := object["generation"].(type) {
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func readDeliveryFile(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(value)
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}
