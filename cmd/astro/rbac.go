// RBAC subcommands: roles (global/cluster/project), bindings, role-bindings,
// templates, and the introspection endpoints (my-permissions, my-roles,
// effective-permissions, permission-preview).
//
// Every subcommand follows the same shape as cluster.go: resolve the typed
// SDK client via the shared newAstroClient helper, call the generated
// WithResponse method, surface a clean error for non-2xx responses, and
// render the typed JSON body honoring the global -o/--output flag through
// renderSDK.

package main

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newRbacCmd is the top-level "astro rbac" command group. A later
// integration step wires this into the root command.
func newRbacCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rbac",
		Aliases: []string{"authz"},
		Short:   "Manage RBAC roles, bindings, and permissions",
	}
	cmd.AddCommand(
		newRbacGlobalRolesCmd(),
		newRbacClusterRolesCmd(),
		newRbacProjectRolesCmd(),
		newRbacGlobalBindingsCmd(),
		newRbacClusterBindingsCmd(),
		newRbacProjectBindingsCmd(),
		newRbacGlobalRoleBindingsCmd(),
		newRbacClusterRoleBindingsCmd(),
		newRbacProjectRoleBindingsCmd(),
		newRbacTemplatesCmd(),
		newRbacMyPermissionsCmd(),
		newRbacMyRolesCmd(),
		newRbacEffectivePermissionsCmd(),
		newRbacPermissionPreviewCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// parseUUID validates an id argument and returns it as the SDK UUID type,
// wrapping the failure in a friendly message.
func parseUUID(label, s string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid %s %q: %w", label, s, err)
	}
	return id, nil
}

// parseUUIDPtr parses an optional UUID flag. An empty string yields nil.
func parseUUIDPtr(label, s string) (*openapi_types.UUID, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	id, err := parseUUID(label, s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// strPtr returns a pointer to s, or nil when s is empty.
func strPtr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// sdkErr maps a non-2xx SDK response (already decoded into an ErrorEnvelope
// on the matching JSONxxx field) to a clean CLI error. It is called when the
// expected success body is nil. The body []byte and HTTP status are used as a
// fallback when no typed envelope decoded.
func sdkErr(action string, status int, env *astroclient.ErrorEnvelope, body []byte) error {
	if env != nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("%s: %s (%s)", action, env.Error.Message, env.Error.Code)
		}
		return fmt.Errorf("%s: %s", action, env.Error.Message)
	}
	if len(body) > 0 {
		return fmt.Errorf("%s: server returned %d: %s", action, status, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%s: server returned status %d", action, status)
}

// roleRequestFlags collects the shared flags for create/update of a role.
type roleRequestFlags struct {
	name        string
	displayName string
	scope       string
}

// build assembles an RBACRoleRequest from the collected flags. The name is
// required; rules are not exposed as flags (use json/yaml input via the API
// directly for fine-grained rule editing — see the note in Report).
func (f roleRequestFlags) build() (astroclient.RBACRoleRequest, error) {
	if strings.TrimSpace(f.name) == "" {
		return astroclient.RBACRoleRequest{}, fmt.Errorf("--name is required")
	}
	req := astroclient.RBACRoleRequest{Name: f.name}
	req.DisplayName = strPtr(f.displayName)
	if s := strings.TrimSpace(f.scope); s != "" {
		sc := astroclient.RBACRoleRequestScope(s)
		req.Scope = &sc
	}
	return req, nil
}

// ---------------------------------------------------------------------
// Global roles
// ---------------------------------------------------------------------

func newRbacGlobalRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "global-roles",
		Aliases: []string{"global-role"},
		Short:   "Manage global RBAC roles",
	}
	cmd.AddCommand(
		newRbacGlobalRolesListCmd(),
		newRbacGlobalRolesGetCmd(),
		newRbacGlobalRolesCreateCmd(),
		newRbacGlobalRolesUpdateCmd(),
		newRbacGlobalRolesDeleteCmd(),
	)
	return cmd
}

func newRbacGlobalRolesListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List global roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacGlobalRolesParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			resp, err := client.GetApiV1RbacGlobalRolesWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list global roles", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	return cmd
}

func newRbacGlobalRolesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one global role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacGlobalRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get global role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newRbacGlobalRolesCreateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a global role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1RbacGlobalRolesWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create global role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacGlobalRolesUpdateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a global role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PutApiV1RbacGlobalRolesIdWithResponse(cmd.Context(), id, req)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("update global role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacGlobalRolesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a global role",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacGlobalRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete global role", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted global role %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Cluster roles
// ---------------------------------------------------------------------

func newRbacClusterRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster-roles",
		Aliases: []string{"cluster-role"},
		Short:   "Manage cluster RBAC roles",
	}
	cmd.AddCommand(
		newRbacClusterRolesListCmd(),
		newRbacClusterRolesGetCmd(),
		newRbacClusterRolesCreateCmd(),
		newRbacClusterRolesUpdateCmd(),
		newRbacClusterRolesDeleteCmd(),
	)
	return cmd
}

func newRbacClusterRolesListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cluster roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacClusterRolesParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			resp, err := client.GetApiV1RbacClusterRolesWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list cluster roles", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	return cmd
}

func newRbacClusterRolesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one cluster role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacClusterRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get cluster role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newRbacClusterRolesCreateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cluster role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1RbacClusterRolesWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create cluster role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacClusterRolesUpdateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a cluster role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PutApiV1RbacClusterRolesIdWithResponse(cmd.Context(), id, req)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("update cluster role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacClusterRolesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a cluster role",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacClusterRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete cluster role", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted cluster role %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Project roles
// ---------------------------------------------------------------------

func newRbacProjectRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project-roles",
		Aliases: []string{"project-role"},
		Short:   "Manage project RBAC roles",
	}
	cmd.AddCommand(
		newRbacProjectRolesListCmd(),
		newRbacProjectRolesGetCmd(),
		newRbacProjectRolesCreateCmd(),
		newRbacProjectRolesUpdateCmd(),
		newRbacProjectRolesDeleteCmd(),
	)
	return cmd
}

func newRbacProjectRolesListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List project roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacProjectRolesParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			resp, err := client.GetApiV1RbacProjectRolesWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list project roles", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	return cmd
}

func newRbacProjectRolesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one project role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacProjectRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get project role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newRbacProjectRolesCreateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1RbacProjectRolesWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create project role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacProjectRolesUpdateCmd() *cobra.Command {
	var f roleRequestFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a project role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			req, err := f.build()
			if err != nil {
				return err
			}
			resp, err := client.PutApiV1RbacProjectRolesIdWithResponse(cmd.Context(), id, req)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("update project role", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addRoleFlags(cmd, &f)
	return cmd
}

func newRbacProjectRolesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a project role",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacProjectRolesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete project role", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted project role %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Bindings (subject -> role, scope-resolved server side)
//
// Two binding families exist in the API and both are exposed:
//   *-bindings        -> /api/v1/rbac/{scope}-bindings
//   *-role-bindings   -> /api/v1/rbac/{scope}-role-bindings
// They share the same request/response shapes; the second family is the
// canonical "role binding" resource. Both are wired so callers can use
// whichever path their environment serves.
// ---------------------------------------------------------------------

// bindingFlags collects the shared subject + role flags for binding create.
type bindingFlags struct {
	roleID string
	userID string
	group  string
}

func (f bindingFlags) buildGlobal() (astroclient.RBACBindingRequest, error) {
	roleID, err := parseUUID("role id", f.roleID)
	if err != nil {
		return astroclient.RBACBindingRequest{}, err
	}
	userID, err := parseUUIDPtr("user id", f.userID)
	if err != nil {
		return astroclient.RBACBindingRequest{}, err
	}
	if userID == nil && strings.TrimSpace(f.group) == "" {
		return astroclient.RBACBindingRequest{}, fmt.Errorf("one of --user-id or --group is required")
	}
	return astroclient.RBACBindingRequest{RoleId: roleID, UserId: userID, Group: strPtr(f.group)}, nil
}

func addBindingFlags(cmd *cobra.Command, f *bindingFlags) {
	cmd.Flags().StringVar(&f.roleID, "role-id", "", "role UUID to bind (required)")
	cmd.Flags().StringVar(&f.userID, "user-id", "", "subject user UUID")
	cmd.Flags().StringVar(&f.group, "group", "", "subject group name")
}

func newRbacGlobalBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "global-bindings",
		Aliases: []string{"global-binding"},
		Short:   "Manage global RBAC bindings",
	}
	cmd.AddCommand(
		newRbacGlobalBindingsListCmd(),
		newRbacGlobalBindingsCreateCmd(),
		newRbacGlobalBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacGlobalBindingsListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List global bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacGlobalBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			resp, err := client.GetApiV1RbacGlobalBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list global bindings", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	return cmd
}

func newRbacGlobalBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a global binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.buildGlobal()
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1RbacGlobalBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create global binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	return cmd
}

func newRbacGlobalBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a global binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacGlobalBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete global binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted global binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newRbacClusterBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster-bindings",
		Aliases: []string{"cluster-binding"},
		Short:   "Manage cluster RBAC bindings",
	}
	cmd.AddCommand(
		newRbacClusterBindingsListCmd(),
		newRbacClusterBindingsCreateCmd(),
		newRbacClusterBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacClusterBindingsListCmd() *cobra.Command {
	var limit, offset int
	var clusterID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cluster bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacClusterBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			if params.ClusterId, err = parseUUIDPtr("cluster id", clusterID); err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacClusterBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list cluster bindings", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "filter by cluster UUID")
	return cmd
}

func newRbacClusterBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	var clusterID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cluster binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			cid, err := parseUUID("cluster id", clusterID)
			if err != nil {
				return err
			}
			roleID, err := parseUUID("role id", f.roleID)
			if err != nil {
				return err
			}
			userID, err := parseUUIDPtr("user id", f.userID)
			if err != nil {
				return err
			}
			if userID == nil && strings.TrimSpace(f.group) == "" {
				return fmt.Errorf("one of --user-id or --group is required")
			}
			req := astroclient.RBACClusterBindingRequest{ClusterId: cid, RoleId: roleID, UserId: userID, Group: strPtr(f.group)}
			resp, err := client.PostApiV1RbacClusterBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create cluster binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "cluster UUID to scope the binding (required)")
	return cmd
}

func newRbacClusterBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a cluster binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacClusterBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete cluster binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted cluster binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newRbacProjectBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project-bindings",
		Aliases: []string{"project-binding"},
		Short:   "Manage project RBAC bindings",
	}
	cmd.AddCommand(
		newRbacProjectBindingsListCmd(),
		newRbacProjectBindingsCreateCmd(),
		newRbacProjectBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacProjectBindingsListCmd() *cobra.Command {
	var limit, offset int
	var projectID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List project bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacProjectBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			if params.ProjectId, err = parseUUIDPtr("project id", projectID); err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacProjectBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list project bindings", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	cmd.Flags().StringVar(&projectID, "project-id", "", "filter by project UUID")
	return cmd
}

func newRbacProjectBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	var projectID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseUUID("project id", projectID)
			if err != nil {
				return err
			}
			roleID, err := parseUUID("role id", f.roleID)
			if err != nil {
				return err
			}
			userID, err := parseUUIDPtr("user id", f.userID)
			if err != nil {
				return err
			}
			if userID == nil && strings.TrimSpace(f.group) == "" {
				return fmt.Errorf("one of --user-id or --group is required")
			}
			req := astroclient.RBACProjectBindingRequest{ProjectId: pid, RoleId: roleID, UserId: userID, Group: strPtr(f.group)}
			resp, err := client.PostApiV1RbacProjectBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create project binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	cmd.Flags().StringVar(&projectID, "project-id", "", "project UUID to scope the binding (required)")
	return cmd
}

func newRbacProjectBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a project binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacProjectBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete project binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted project binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Role-bindings (canonical resource family)
// ---------------------------------------------------------------------

func newRbacGlobalRoleBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "global-role-bindings",
		Aliases: []string{"global-role-binding"},
		Short:   "Manage global RBAC role-bindings",
	}
	cmd.AddCommand(
		newRbacGlobalRoleBindingsListCmd(),
		newRbacGlobalRoleBindingsCreateCmd(),
		newRbacGlobalRoleBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacGlobalRoleBindingsListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List global role-bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacGlobalRoleBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			resp, err := client.GetApiV1RbacGlobalRoleBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list global role-bindings", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	return cmd
}

func newRbacGlobalRoleBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a global role-binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.buildGlobal()
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1RbacGlobalRoleBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create global role-binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	return cmd
}

func newRbacGlobalRoleBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a global role-binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role-binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacGlobalRoleBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete global role-binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted global role-binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newRbacClusterRoleBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster-role-bindings",
		Aliases: []string{"cluster-role-binding"},
		Short:   "Manage cluster RBAC role-bindings",
	}
	cmd.AddCommand(
		newRbacClusterRoleBindingsListCmd(),
		newRbacClusterRoleBindingsCreateCmd(),
		newRbacClusterRoleBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacClusterRoleBindingsListCmd() *cobra.Command {
	var limit, offset int
	var clusterID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cluster role-bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacClusterRoleBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			if params.ClusterId, err = parseUUIDPtr("cluster id", clusterID); err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacClusterRoleBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list cluster role-bindings", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "filter by cluster UUID")
	return cmd
}

func newRbacClusterRoleBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	var clusterID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cluster role-binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			cid, err := parseUUID("cluster id", clusterID)
			if err != nil {
				return err
			}
			roleID, err := parseUUID("role id", f.roleID)
			if err != nil {
				return err
			}
			userID, err := parseUUIDPtr("user id", f.userID)
			if err != nil {
				return err
			}
			if userID == nil && strings.TrimSpace(f.group) == "" {
				return fmt.Errorf("one of --user-id or --group is required")
			}
			req := astroclient.RBACClusterBindingRequest{ClusterId: cid, RoleId: roleID, UserId: userID, Group: strPtr(f.group)}
			resp, err := client.PostApiV1RbacClusterRoleBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create cluster role-binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "cluster UUID to scope the binding (required)")
	return cmd
}

func newRbacClusterRoleBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a cluster role-binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role-binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacClusterRoleBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete cluster role-binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted cluster role-binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newRbacProjectRoleBindingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project-role-bindings",
		Aliases: []string{"project-role-binding"},
		Short:   "Manage project RBAC role-bindings",
	}
	cmd.AddCommand(
		newRbacProjectRoleBindingsListCmd(),
		newRbacProjectRoleBindingsCreateCmd(),
		newRbacProjectRoleBindingsDeleteCmd(),
	)
	return cmd
}

func newRbacProjectRoleBindingsListCmd() *cobra.Command {
	var limit, offset int
	var projectID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List project role-bindings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacProjectRoleBindingsParams{}
			applyLimitOffset(&params.Limit, &params.Offset, limit, offset)
			if params.ProjectId, err = parseUUIDPtr("project id", projectID); err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacProjectRoleBindingsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list project role-bindings", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	addLimitOffsetFlags(cmd, &limit, &offset)
	cmd.Flags().StringVar(&projectID, "project-id", "", "filter by project UUID")
	return cmd
}

func newRbacProjectRoleBindingsCreateCmd() *cobra.Command {
	var f bindingFlags
	var projectID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project role-binding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseUUID("project id", projectID)
			if err != nil {
				return err
			}
			roleID, err := parseUUID("role id", f.roleID)
			if err != nil {
				return err
			}
			userID, err := parseUUIDPtr("user id", f.userID)
			if err != nil {
				return err
			}
			if userID == nil && strings.TrimSpace(f.group) == "" {
				return fmt.Errorf("one of --user-id or --group is required")
			}
			req := astroclient.RBACProjectBindingRequest{ProjectId: pid, RoleId: roleID, UserId: userID, Group: strPtr(f.group)}
			resp, err := client.PostApiV1RbacProjectRoleBindingsWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return sdkErr("create project role-binding", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	addBindingFlags(cmd, &f)
	cmd.Flags().StringVar(&projectID, "project-id", "", "project UUID to scope the binding (required)")
	return cmd
}

func newRbacProjectRoleBindingsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a project role-binding",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseUUID("role-binding id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1RbacProjectRoleBindingsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return sdkErr("delete project role-binding", sc, firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON404), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted project role-binding %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------

func newRbacTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"template"},
		Short:   "Inspect built-in RBAC role templates",
	}
	cmd.AddCommand(
		newRbacTemplatesListCmd(),
		newRbacTemplatesGetCmd(),
	)
	return cmd
}

func newRbacTemplatesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List RBAC role templates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacTemplatesWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("list templates", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON503), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newRbacTemplatesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Show one RBAC role template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacTemplatesNameWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get template", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON404, resp.JSON503), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Introspection: my-permissions / my-roles / effective-permissions / preview
// ---------------------------------------------------------------------

func newRbacMyPermissionsCmd() *cobra.Command {
	var clusterID, projectID, namespace string
	cmd := &cobra.Command{
		Use:   "my-permissions",
		Short: "Show the calling user's effective permissions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacMyPermissionsParams{}
			if params.ClusterId, err = parseUUIDPtr("cluster id", clusterID); err != nil {
				return err
			}
			if params.ProjectId, err = parseUUIDPtr("project id", projectID); err != nil {
				return err
			}
			params.Namespace = strPtr(namespace)
			resp, err := client.GetApiV1RbacMyPermissionsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get my permissions", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON500, resp.JSON503), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "scope to a cluster UUID")
	cmd.Flags().StringVar(&projectID, "project-id", "", "scope to a project UUID")
	cmd.Flags().StringVar(&namespace, "namespace", "", "scope to a namespace")
	return cmd
}

func newRbacMyRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "my-roles",
		Short: "Show the calling user's roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacMyRolesWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get my roles", resp.StatusCode(), firstEnv(resp.JSON401, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.AddCommand(newRbacMyRolesCheckCmd())
	return cmd
}

func newRbacMyRolesCheckCmd() *cobra.Command {
	var resource, verb, clusterID, projectID string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether the calling user can perform <verb> on <resource>",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(resource) == "" || strings.TrimSpace(verb) == "" {
				return fmt.Errorf("--resource and --verb are required")
			}
			params := &astroclient.GetApiV1RbacMyRolesCheckParams{Resource: resource, Verb: verb}
			if params.ClusterId, err = parseUUIDPtr("cluster id", clusterID); err != nil {
				return err
			}
			if params.ProjectId, err = parseUUIDPtr("project id", projectID); err != nil {
				return err
			}
			resp, err := client.GetApiV1RbacMyRolesCheckWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("check my permission", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&resource, "resource", "", "resource to check (required)")
	cmd.Flags().StringVar(&verb, "verb", "", "verb to check, e.g. get/list/create/delete (required)")
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "scope to a cluster UUID")
	cmd.Flags().StringVar(&projectID, "project-id", "", "scope to a project UUID")
	return cmd
}

func newRbacEffectivePermissionsCmd() *cobra.Command {
	var clusterID, projectID, namespace string
	cmd := &cobra.Command{
		Use:   "effective-permissions <user-id>",
		Short: "Show a user's effective permissions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			userID, err := parseUUID("user id", args[0])
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1RbacEffectivePermissionsUserIdParams{}
			if params.ClusterId, err = parseUUIDPtr("cluster id", clusterID); err != nil {
				return err
			}
			if params.ProjectId, err = parseUUIDPtr("project id", projectID); err != nil {
				return err
			}
			params.Namespace = strPtr(namespace)
			resp, err := client.GetApiV1RbacEffectivePermissionsUserIdWithResponse(cmd.Context(), userID, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("get effective permissions", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403, resp.JSON500, resp.JSON503), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "scope to a cluster UUID")
	cmd.Flags().StringVar(&projectID, "project-id", "", "scope to a project UUID")
	cmd.Flags().StringVar(&namespace, "namespace", "", "scope to a namespace")
	return cmd
}

func newRbacPermissionPreviewCmd() *cobra.Command {
	var roleID, templateName, scope string
	cmd := &cobra.Command{
		Use:     "permission-preview",
		Aliases: []string{"permission-check"},
		Short:   "Preview the permissions a role or template would grant",
		Long: `Posts to /api/v1/rbac/permission-preview and prints the resolved
grants, risk level, and any warnings. Supply one of --role-id or
--template-name; --scope is optional. Fine-grained ad-hoc rule lists
are accepted by the API but are not exposed as flags here (see Report).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			req := astroclient.RBACPermissionPreviewRequest{}
			if rid, err := parseUUIDPtr("role id", roleID); err != nil {
				return err
			} else {
				req.RoleId = rid
			}
			req.TemplateName = strPtr(templateName)
			if s := strings.TrimSpace(scope); s != "" {
				sc := astroclient.RBACPermissionPreviewRequestScope(s)
				req.Scope = &sc
			}
			if req.RoleId == nil && req.TemplateName == nil {
				return fmt.Errorf("one of --role-id or --template-name is required")
			}
			resp, err := client.PostApiV1RbacPermissionPreviewWithResponse(cmd.Context(), req)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return sdkErr("permission preview", resp.StatusCode(), firstEnv(resp.JSON400, resp.JSON401, resp.JSON403), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&roleID, "role-id", "", "preview an existing role by UUID")
	cmd.Flags().StringVar(&templateName, "template-name", "", "preview a built-in template by name")
	cmd.Flags().StringVar(&scope, "scope", "", "scope hint (global|cluster|project)")
	return cmd
}

// ---------------------------------------------------------------------
// Flag helpers shared across list commands
// ---------------------------------------------------------------------

func addLimitOffsetFlags(cmd *cobra.Command, limit, offset *int) {
	cmd.Flags().IntVar(limit, "limit", 0, "max results to return (0 = server default)")
	cmd.Flags().IntVar(offset, "offset", 0, "results offset for pagination")
}

// applyLimitOffset copies non-zero limit/offset values into the pointer
// fields the generated params structs expose.
func applyLimitOffset(dstLimit, dstOffset **int, limit, offset int) {
	if limit > 0 {
		l := limit
		*dstLimit = &l
	}
	if offset > 0 {
		o := offset
		*dstOffset = &o
	}
}

func addRoleFlags(cmd *cobra.Command, f *roleRequestFlags) {
	cmd.Flags().StringVar(&f.name, "name", "", "role name (required)")
	cmd.Flags().StringVar(&f.displayName, "display-name", "", "human-readable name")
	cmd.Flags().StringVar(&f.scope, "scope", "", "role scope (global|cluster|project)")
}

// firstEnv returns the first non-nil ErrorEnvelope from the candidates, so
// callers can pass every possible error field for an endpoint and let this
// pick whichever one the SDK populated.
func firstEnv(envs ...*astroclient.ErrorEnvelope) *astroclient.ErrorEnvelope {
	for _, e := range envs {
		if e != nil {
			return e
		}
	}
	return nil
}
