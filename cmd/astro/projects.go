// Project subcommands: list / get / create / update / delete / clusters /
// add-namespace / remove-namespace / quota / policy / takeover, plus a
// cloud-credentials child group (list/get/create/update/delete/test).
//
// Every leaf builds the generated SDK client via the shared newAstroClient
// foundation helper, calls the typed *WithResponse method, and renders the
// `data` envelope through renderSDK so the global -o/--output flag is
// honored. Non-2xx responses are mapped to a clean error + non-zero exit.
//
// Style mirrors cluster.go: one constructor per subcommand, flags declared
// locally, RunE closures that return errors rather than calling os.Exit.

package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newProjectsCmd is the `astro projects` command group. A later
// integration step wires this into the root command; it is intentionally
// self-contained here.
//
// (The orchestrator brief referred to this constructor by a templated
// placeholder name; the real, compilable identifier is newProjectsCmd,
// matching cluster.go's newClusterCmd convention.)
func newProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "Manage projects (cluster-scoped namespace policy containers)",
	}
	cmd.AddCommand(
		newProjectListCmd(),
		newProjectGetCmd(),
		newProjectCreateCmd(),
		newProjectUpdateCmd(),
		newProjectDeleteCmd(),
		newProjectClustersCmd(),
		newProjectAddNamespaceCmd(),
		newProjectRemoveNamespaceCmd(),
		newProjectQuotaCmd(),
		newProjectPolicyCmd(),
		newProjectTakeoverCmd(),
		newProjectCloudCredentialsCmd(),
	)
	return cmd
}

// parseProjectUUID converts a positional argument into the
// openapi_types.UUID the generated SDK methods require, returning a clean
// error on bad input. Named with a projects_ prefix to avoid colliding with
// the equivalent helper other (not-yet-integrated) command groups define.
func parseProjectUUID(label, raw string) (openapi_types.UUID, error) {
	// openapi_types.UUID is an alias for github.com/google/uuid.UUID.
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid %s %q: must be a UUID", label, raw)
	}
	return id, nil
}

// strPtr / intPtr return pointers for the optional request-body fields the
// SDK models as *string / *int. Empty strings / negative ints mean "unset".
func projectStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func projectIntPtr(i int) *int {
	if i < 0 {
		return nil
	}
	return &i
}

// projectStatusCode returns the HTTP status code from a possibly-nil response.
func projectStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// projectAPIError builds a clean error for a non-2xx SDK response. It
// surfaces the HTTP status plus the raw body (the management API returns a
// JSON error envelope) so callers see a useful message and a non-zero exit.
func projectAPIError(action string, resp *http.Response, body []byte) error {
	status := "unknown status"
	if resp != nil {
		status = resp.Status
	}
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("%s failed: %s", action, status)
	}
	return fmt.Errorf("%s failed: %s: %s", action, status, detail)
}

// ---------------------------------------------------------------------------
// projects list
// ---------------------------------------------------------------------------

func newProjectListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1ProjectsParams{}
			if limit >= 0 {
				params.Limit = &limit
			}
			if offset >= 0 {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1ProjectsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("list projects", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", -1, "page size (default server-side 20)")
	cmd.Flags().IntVar(&offset, "offset", -1, "row offset into the result set")
	return cmd
}

// ---------------------------------------------------------------------------
// projects get <id>
// ---------------------------------------------------------------------------

func newProjectGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one project's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ProjectsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("get project", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// projects create <name>
// ---------------------------------------------------------------------------

func newProjectCreateCmd() *cobra.Command {
	var clusterID, description, podSecurityProfile string
	var namespaces []string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project in a cluster",
		Long: `create posts to /api/v1/projects/. A project is scoped to one
cluster (--cluster is required) and may seed an initial namespace set.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			cid, err := parseProjectUUID("cluster id", clusterID)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1ProjectsJSONRequestBody{
				Name:               args[0],
				ClusterId:          cid,
				Description:        projectStrPtr(description),
				PodSecurityProfile: projectStrPtr(podSecurityProfile),
			}
			if len(namespaces) > 0 {
				body.Namespaces = &namespaces
			}
			resp, err := client.PostApiV1ProjectsWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return projectAPIError("create project", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster", "", "cluster UUID the project lives in (required)")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringVar(&podSecurityProfile, "pod-security-profile", "", "pod security profile to apply")
	cmd.Flags().StringSliceVar(&namespaces, "namespace", nil, "initial namespace (repeatable)")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

// ---------------------------------------------------------------------------
// projects update <id>
// ---------------------------------------------------------------------------

func newProjectUpdateCmd() *cobra.Command {
	var name, description, podSecurityProfile string
	var quotaCPU, quotaMemory string
	var quotaPods int
	var namespaces []string
	var replaceNamespaces bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a project (PUT — full replace of the provided fields)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			body := astroclient.PutApiV1ProjectsIdJSONRequestBody{
				Name:                projectStrPtr(name),
				Description:         projectStrPtr(description),
				PodSecurityProfile:  projectStrPtr(podSecurityProfile),
				ResourceQuotaCpu:    projectStrPtr(quotaCPU),
				ResourceQuotaMemory: projectStrPtr(quotaMemory),
				ResourceQuotaPods:   projectIntPtr(quotaPods),
			}
			if replaceNamespaces {
				// Allow clearing to an explicit (possibly empty) set.
				ns := namespaces
				body.Namespaces = &ns
			} else if len(namespaces) > 0 {
				body.Namespaces = &namespaces
			}
			resp, err := client.PutApiV1ProjectsIdWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("update project", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&podSecurityProfile, "pod-security-profile", "", "pod security profile")
	cmd.Flags().StringVar(&quotaCPU, "quota-cpu", "", "resource quota CPU (e.g. 4)")
	cmd.Flags().StringVar(&quotaMemory, "quota-memory", "", "resource quota memory (e.g. 8Gi)")
	cmd.Flags().IntVar(&quotaPods, "quota-pods", -1, "resource quota max pods")
	cmd.Flags().StringSliceVar(&namespaces, "namespace", nil, "namespace (repeatable)")
	cmd.Flags().BoolVar(&replaceNamespaces, "replace-namespaces", false, "send the namespace set even when empty (full replace)")
	return cmd
}

// ---------------------------------------------------------------------------
// projects delete <id>
// ---------------------------------------------------------------------------

func newProjectDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a project",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1ProjectsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := projectStatusCode(resp.HTTPResponse); sc < 200 || sc >= 300 {
				return projectAPIError("delete project", resp.HTTPResponse, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// projects clusters <id>
// ---------------------------------------------------------------------------

func newProjectClustersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clusters <id>",
		Short: "List clusters (and namespace counts) backing a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ProjectsIdClustersWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("list project clusters", resp.HTTPResponse, resp.Body)
			}
			// The clusters array is the interesting payload; fall back to
			// the whole data object when it is absent.
			if resp.JSON200.Data.Clusters != nil {
				return renderSDK(cmd, *resp.JSON200.Data.Clusters)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// projects add-namespace <id>
// ---------------------------------------------------------------------------

func newProjectAddNamespaceCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "add-namespace <id>",
		Short: "Attach a namespace to a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}
			body := astroclient.PostApiV1ProjectsIdAddNamespaceJSONRequestBody{Namespace: namespace}
			resp, err := client.PostApiV1ProjectsIdAddNamespaceWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("add namespace", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace name to attach (required)")
	_ = cmd.MarkFlagRequired("namespace")
	return cmd
}

// ---------------------------------------------------------------------------
// projects remove-namespace <id>
// ---------------------------------------------------------------------------

func newProjectRemoveNamespaceCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "remove-namespace <id>",
		Short: "Detach a namespace from a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}
			body := astroclient.PostApiV1ProjectsIdRemoveNamespaceJSONRequestBody{Namespace: namespace}
			resp, err := client.PostApiV1ProjectsIdRemoveNamespaceWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("remove namespace", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace name to detach (required)")
	_ = cmd.MarkFlagRequired("namespace")
	return cmd
}

// ---------------------------------------------------------------------------
// projects quota <id>
// ---------------------------------------------------------------------------

func newProjectQuotaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota <id>",
		Short: "Show resource-quota usage for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ProjectsIdQuotaUsageWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("quota usage", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// projects policy <id>
// ---------------------------------------------------------------------------

func newProjectPolicyCmd() *cobra.Command {
	var podSecurityProfile, quotaCPU, quotaMemory string
	var quotaPods int
	cmd := &cobra.Command{
		Use:   "policy <id>",
		Short: "Patch a project's policy (pod security profile + resource quotas)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			body := astroclient.PatchApiV1ProjectsIdPolicyJSONRequestBody{
				PodSecurityProfile:  projectStrPtr(podSecurityProfile),
				ResourceQuotaCpu:    projectStrPtr(quotaCPU),
				ResourceQuotaMemory: projectStrPtr(quotaMemory),
				ResourceQuotaPods:   projectIntPtr(quotaPods),
			}
			resp, err := client.PatchApiV1ProjectsIdPolicyWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("patch policy", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&podSecurityProfile, "pod-security-profile", "", "pod security profile")
	cmd.Flags().StringVar(&quotaCPU, "quota-cpu", "", "resource quota CPU (e.g. 4)")
	cmd.Flags().StringVar(&quotaMemory, "quota-memory", "", "resource quota memory (e.g. 8Gi)")
	cmd.Flags().IntVar(&quotaPods, "quota-pods", -1, "resource quota max pods")
	return cmd
}

// ---------------------------------------------------------------------------
// projects takeover <id>
// ---------------------------------------------------------------------------

func newProjectTakeoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "takeover <id>",
		Short: "Take ownership of a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ProjectsIdOwnershipTakeoverWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("ownership takeover", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// projects cloud-credentials ...
// ---------------------------------------------------------------------------

func newProjectCloudCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cloud-credentials",
		Aliases: []string{"cloud-creds", "credentials"},
		Short:   "Manage a project's cloud-provider credentials",
	}
	cmd.AddCommand(
		newCloudCredentialsListCmd(),
		newCloudCredentialsGetCmd(),
		newCloudCredentialsCreateCmd(),
		newCloudCredentialsUpdateCmd(),
		newCloudCredentialsDeleteCmd(),
		newCloudCredentialsTestCmd(),
	)
	return cmd
}

func newCloudCredentialsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <project-id>",
		Short: "List a project's cloud credentials (secret values redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ProjectsProjectIdCloudCredentialsWithResponse(cmd.Context(), pid)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("list cloud credentials", resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200.Data.Items != nil {
				return renderSDK(cmd, *resp.JSON200.Data.Items)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newCloudCredentialsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <project-id> <id>",
		Short: "Show one cloud credential",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("credential id", args[1])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ProjectsProjectIdCloudCredentialsIdWithResponse(cmd.Context(), pid, id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("get cloud credential", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newCloudCredentialsCreateCmd() *cobra.Command {
	var name, provider, description string
	var data map[string]string
	cmd := &cobra.Command{
		Use:   "create <project-id>",
		Short: "Create a cloud credential for a project",
		Long: `create posts a provider credential. Secret fields are passed via
repeatable --data key=value pairs (e.g. --data access_key_id=... --data
secret_access_key=...). Server redacts secret values on read-back.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			if name == "" || provider == "" {
				return fmt.Errorf("--name and --provider are required")
			}
			body := astroclient.PostApiV1ProjectsProjectIdCloudCredentialsJSONRequestBody{
				Name:        name,
				Provider:    provider,
				Description: projectStrPtr(description),
			}
			if len(data) > 0 {
				d := data
				body.Data = &d
			}
			resp, err := client.PostApiV1ProjectsProjectIdCloudCredentialsWithResponse(cmd.Context(), pid, body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return projectAPIError("create cloud credential", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "credential name (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "cloud provider, e.g. aws|gcp|azure (required)")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringToStringVar(&data, "data", nil, "secret key=value (repeatable)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}

func newCloudCredentialsUpdateCmd() *cobra.Command {
	var name, provider, description string
	var data map[string]string
	var usePatch bool
	cmd := &cobra.Command{
		Use:   "update <project-id> <id>",
		Short: "Update a cloud credential (PUT by default; --patch for partial)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("credential id", args[1])
			if err != nil {
				return err
			}
			body := astroclient.CloudCredentialRequest{
				Name:        name,
				Provider:    provider,
				Description: projectStrPtr(description),
			}
			if len(data) > 0 {
				d := data
				body.Data = &d
			}
			if usePatch {
				resp, err := client.PatchApiV1ProjectsProjectIdCloudCredentialsIdWithResponse(cmd.Context(), pid, id, body)
				if err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return projectAPIError("patch cloud credential", resp.HTTPResponse, resp.Body)
				}
				return renderSDK(cmd, resp.JSON200.Data)
			}
			// PUT requires the full object; name + provider are mandatory.
			if name == "" || provider == "" {
				return fmt.Errorf("--name and --provider are required for a PUT update (use --patch for partial updates)")
			}
			resp, err := client.PutApiV1ProjectsProjectIdCloudCredentialsIdWithResponse(cmd.Context(), pid, id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("update cloud credential", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "credential name")
	cmd.Flags().StringVar(&provider, "provider", "", "cloud provider")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringToStringVar(&data, "data", nil, "secret key=value (repeatable)")
	cmd.Flags().BoolVar(&usePatch, "patch", false, "use PATCH (partial update) instead of PUT")
	return cmd
}

func newCloudCredentialsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <project-id> <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a cloud credential",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("credential id", args[1])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1ProjectsProjectIdCloudCredentialsIdWithResponse(cmd.Context(), pid, id)
			if err != nil {
				return err
			}
			if sc := projectStatusCode(resp.HTTPResponse); sc < 200 || sc >= 300 {
				return projectAPIError("delete cloud credential", resp.HTTPResponse, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted cloud credential %s.\n", args[1])
			return err
		},
	}
	return cmd
}

func newCloudCredentialsTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <project-id> <id>",
		Short: "Test a cloud credential against its provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			pid, err := parseProjectUUID("project id", args[0])
			if err != nil {
				return err
			}
			id, err := parseProjectUUID("credential id", args[1])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1ProjectsProjectIdCloudCredentialsIdTestWithResponse(cmd.Context(), pid, id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return projectAPIError("test cloud credential", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}
