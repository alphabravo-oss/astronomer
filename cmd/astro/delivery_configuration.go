package main

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

// newDeliveryConfigurationTemplateCmd exposes the same generation-fenced,
// secret-reference-only template workflow as the web UI. Payloads are read
// through readJSONInput, so operators can use @file or stdin without placing
// sensitive references in shell history.
func newDeliveryConfigurationTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "configuration-template", Aliases: []string{"template"}, Short: "Manage reusable delivery configuration templates"}
	cmd.AddCommand(
		newDeliveryListCmd("configuration template", "/api/v1/delivery/configuration-templates/"),
		newDeliveryGetCmd("configuration template", "/api/v1/delivery/configuration-templates/%s/"),
		newDeliveryMutationCmd("create", "Create a configuration template", http.MethodPost, "/api/v1/delivery/configuration-templates/", false),
		newDeliveryConfigurationTemplateUpdateCmd(),
		newDeliveryConfigurationTemplateDeleteCmd(),
	)
	return cmd
}

func newDeliveryOverrideSetCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "override-set", Short: "Manage deterministic scoped configuration overrides"}
	cmd.AddCommand(
		newDeliveryListCmd("override set", "/api/v1/delivery/override-sets/"),
		newDeliveryGetCmd("override set", "/api/v1/delivery/override-sets/%s/"),
		newDeliveryMutationCmd("create", "Create a scoped override set", http.MethodPost, "/api/v1/delivery/override-sets/", false),
		newDeliveryGenerationFencedUpdateCmd("override set", "/api/v1/delivery/override-sets/%s/"),
		newDeliveryGenerationFencedDeleteCmd("override set", "/api/v1/delivery/override-sets/%s/"),
		newDeliveryEffectiveConfigurationCmd(),
	)
	return cmd
}

func newDeliveryEffectiveConfigurationCmd() *cobra.Command {
	var project, body string
	cmd := &cobra.Command{
		Use: "effective", Short: "Resolve effective values, patches, layer order, and digest", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			payload, err := readJSONInput(body)
			if err != nil {
				return err
			}
			return runAPICommand(cmd, http.MethodPost, "/api/v1/delivery/override-sets/effective/?project_id="+url.QueryEscape(projectID), payload, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "base_values and override_ids JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

func newDeliveryGenerationFencedUpdateCmd(noun, pathTemplate string) *cobra.Command {
	var project, body string
	cmd := &cobra.Command{Use: "update <id>", Short: "Update a delivery " + noun + " with optimistic concurrency", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireUUIDFlag("project", project)
		if err != nil {
			return err
		}
		if _, err := parseUUID("id", args[0]); err != nil {
			return err
		}
		payload, err := readJSONInput(body)
		if err != nil {
			return err
		}
		path := fmt.Sprintf(pathTemplate, args[0]) + "?project_id=" + url.QueryEscape(projectID)
		current, err := getDeliveryObject(cmd, path)
		if err != nil {
			return err
		}
		generation := deliveryGeneration(current)
		if generation < 1 {
			return fmt.Errorf("%s has no valid generation", noun)
		}
		return runAPICommandWithHeaders(cmd, http.MethodPut, path, payload, map[string]string{"If-Match": quotedEntityTag(generation)}, "")
	}}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "request JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

func newDeliveryGenerationFencedDeleteCmd(noun, pathTemplate string) *cobra.Command {
	var project string
	var yes bool
	cmd := &cobra.Command{Use: "delete <id>", Short: "Delete a delivery " + noun + " with optimistic concurrency", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		path := fmt.Sprintf(pathTemplate, args[0]) + "?project_id=" + url.QueryEscape(projectID)
		current, err := getDeliveryObject(cmd, path)
		if err != nil {
			return err
		}
		generation := deliveryGeneration(current)
		if generation < 1 {
			return fmt.Errorf("%s has no valid generation", noun)
		}
		return runAPICommandWithHeaders(cmd, http.MethodDelete, path, nil, map[string]string{"If-Match": quotedEntityTag(generation)}, "")
	}}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without a prompt")
	return cmd
}

func newDeliveryConfigurationTemplateUpdateCmd() *cobra.Command {
	var project, body string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a configuration template with optimistic concurrency",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			payload, err := readJSONInput(body)
			if err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/delivery/configuration-templates/%s/?project_id=%s", args[0], url.QueryEscape(projectID))
			current, err := getDeliveryObject(cmd, path)
			if err != nil {
				return err
			}
			generation := deliveryGeneration(current)
			if generation < 1 {
				return fmt.Errorf("configuration template has no valid generation")
			}
			return runAPICommandWithHeaders(cmd, http.MethodPut, path, payload, map[string]string{"If-Match": quotedEntityTag(generation)}, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&body, "data", "", "request JSON, @file, or - for stdin")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

func newDeliveryConfigurationTemplateDeleteCmd() *cobra.Command {
	var project string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a configuration template with optimistic concurrency",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDestructiveConfirmation(cmd, yes, "delete delivery configuration template "+args[0]); err != nil {
				return err
			}
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("id", args[0]); err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/delivery/configuration-templates/%s/?project_id=%s", args[0], url.QueryEscape(projectID))
			current, err := getDeliveryObject(cmd, path)
			if err != nil {
				return err
			}
			generation := deliveryGeneration(current)
			if generation < 1 {
				return fmt.Errorf("configuration template has no valid generation")
			}
			return runAPICommandWithHeaders(cmd, http.MethodDelete, path, nil, map[string]string{"If-Match": quotedEntityTag(generation)}, "")
		},
	}
	addDeliveryProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without a prompt")
	return cmd
}

func newDeliveryInventoryCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{Use: "inventory", Short: "Inspect delivery and system-component inventory"}
	cluster := &cobra.Command{
		Use:   "cluster <cluster-id>",
		Short: "Show Flux deployments and observed system components for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := requireUUIDFlag("project", project)
			if err != nil {
				return err
			}
			if _, err := parseUUID("cluster-id", args[0]); err != nil {
				return err
			}
			query := url.Values{"project_id": []string{projectID}}
			return runAPICommand(cmd, http.MethodGet, fmt.Sprintf("/api/v1/delivery/clusters/%s/inventory/?%s", args[0], query.Encode()), nil, "")
		},
	}
	addDeliveryProjectFlag(cluster, &project)
	fleet := &cobra.Command{
		Use:   "fleet",
		Short: "Show fleet-wide delivery health and compatibility",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAPICommand(cmd, http.MethodGet, "/api/v1/delivery/fleet/", nil, "")
		},
	}
	cmd.AddCommand(cluster, fleet)
	return cmd
}
