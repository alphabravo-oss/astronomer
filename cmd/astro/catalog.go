// Catalog subcommands: charts, repositories, installed charts, operations,
// and controller status. This group talks to the generated pkg/astroclient
// SDK via the shared newAstroClient/renderSDK helpers in client.go.
//
// NOTE on the placeholder names in the task spec: the requested file path
// ("cmd/astro/cmd/astro/catalog.go.go") and function name
// ("newcmd/astro/catalog.goCmd") are obviously templated/mangled. This file
// lives at the natural location cmd/astro/catalog.go and exposes the
// constructor newCatalogCmd(), matching cluster.go's newClusterCmd() style.
// A later integration step wires newCatalogCmd() into the root command.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newCatalogCmd builds the "catalog" command group.
func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "catalog",
		Aliases: []string{"cat"},
		Short:   "Browse and manage the Helm catalog (charts, repositories, installs, operations)",
	}
	cmd.AddCommand(
		newCatalogChartsCmd(),
		newCatalogRepositoriesCmd(),
		newCatalogInstalledCmd(),
		newCatalogOperationsCmd(),
		newCatalogControllerStatusCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// catalogUUID validates an id argument and returns a clean error otherwise.
func catalogUUID(arg string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(arg))
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid id %q: must be a UUID", arg)
	}
	return id, nil
}

// catStrPtr returns a pointer to s, or nil when s is empty. Used to build the
// optional fields of SDK request bodies / query params.
func catStrPtr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// catalogError maps a non-2xx SDK response to a clean error. It prefers the
// typed ErrorEnvelope when present, otherwise falls back to the HTTP status
// and any raw body. ok reports whether the response was a success the caller
// can render.
func catalogError(status string, code int, envelopes ...*astroclient.ErrorEnvelope) error {
	for _, e := range envelopes {
		if e != nil {
			msg := strings.TrimSpace(e.Error.Message)
			if msg == "" {
				msg = e.Error.Code
			}
			if e.Error.Code != "" && e.Error.Code != msg {
				return fmt.Errorf("%s (%s)", msg, e.Error.Code)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("request failed: %s", status)
	}
	return fmt.Errorf("request failed with status %d", code)
}

// ---------------------------------------------------------------------
// charts
// ---------------------------------------------------------------------

func newCatalogChartsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "charts",
		Aliases: []string{"chart"},
		Short:   "Browse catalog charts",
	}
	cmd.AddCommand(
		newCatalogChartsListCmd(),
		newCatalogChartsGetCmd(),
		newCatalogChartReadmeCmd(),
		newCatalogChartValuesCmd(),
		newCatalogChartVersionsCmd(),
	)
	return cmd
}

func newCatalogChartsListCmd() *cobra.Command {
	var tag string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog charts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogChartsParams{Tag: catStrPtr(tag)}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1CatalogChartsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				return writeChartsTable(w, resp.JSON200.Data)
			})
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "filter charts by tag")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	return cmd
}

func newCatalogChartsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one chart's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogChartsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return render(cmd, resp.JSON200.Data, func(w io.Writer) error {
				return writeChartsTable(w, []astroclient.HelmChart{resp.JSON200.Data})
			})
		},
	}
	return cmd
}

func newCatalogChartReadmeCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "readme <id>",
		Short: "Print a chart's README",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogChartsIdReadmeParams{Version: catStrPtr(version)}
			resp, err := client.GetApiV1CatalogChartsIdReadmeWithResponse(cmd.Context(), id, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				if resp.JSON200.Readme != nil {
					_, err := io.WriteString(w, *resp.JSON200.Readme)
					return err
				}
				_, err := fmt.Fprintln(w, "(no README)")
				return err
			})
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "specific chart version (defaults to latest)")
	return cmd
}

func newCatalogChartValuesCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "values <id>",
		Short: "Print a chart's default values.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogChartsIdValuesParams{Version: catStrPtr(version)}
			resp, err := client.GetApiV1CatalogChartsIdValuesWithResponse(cmd.Context(), id, params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				if resp.JSON200.DefaultValues != nil {
					_, err := io.WriteString(w, *resp.JSON200.DefaultValues)
					return err
				}
				_, err := fmt.Fprintln(w, "(no default values)")
				return err
			})
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "specific chart version (defaults to latest)")
	return cmd
}

func newCatalogChartVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions <id>",
		Short: "List a chart's available versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogChartsIdVersionsWithResponse(cmd.Context(), id, nil)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				return writeChartVersionsTable(w, *resp.JSON200)
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// repositories
// ---------------------------------------------------------------------

func newCatalogRepositoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repositories",
		Aliases: []string{"repository", "repos", "repo"},
		Short:   "Manage Helm repositories",
	}
	cmd.AddCommand(
		newCatalogReposListCmd(),
		newCatalogReposGetCmd(),
		newCatalogReposCreateCmd(),
		newCatalogReposUpdateCmd(),
		newCatalogReposDeleteCmd(),
		newCatalogRepoSyncCmd(),
		newCatalogRepoTestConnectionCmd(),
	)
	return cmd
}

func newCatalogReposListCmd() *cobra.Command {
	var limit, offset int
	var includeProjectOwned bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Helm repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogRepositoriesParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			if cmd.Flags().Changed("include-project-owned") {
				params.IncludeProjectOwned = &includeProjectOwned
			}
			resp, err := client.GetApiV1CatalogRepositoriesWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			// The 200 body is a generated oneOf union whose inner field is
			// unexported, so we re-decode the raw body. The server returns
			// either a paginated envelope {count,data:[...]} or a bare list
			// [...]; handle both.
			repos, err := decodeRepoList(resp.Body)
			if err != nil {
				return err
			}
			return render(cmd, repos, func(w io.Writer) error {
				return writeReposTable(w, repos)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	cmd.Flags().BoolVar(&includeProjectOwned, "include-project-owned", false, "include project-owned repositories")
	return cmd
}

// decodeRepoList copes with the repositories-list oneOf: either a paginated
// envelope or a bare array.
func decodeRepoList(body []byte) ([]astroclient.HelmRepository, error) {
	var env struct {
		Data []astroclient.HelmRepository `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Data != nil {
		return env.Data, nil
	}
	var list []astroclient.HelmRepository
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode repositories response: %w", err)
	}
	return list, nil
}

func newCatalogReposGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one repository's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogRepositoriesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return render(cmd, resp.JSON200.Data, func(w io.Writer) error {
				return writeReposTable(w, []astroclient.HelmRepository{resp.JSON200.Data})
			})
		},
	}
	return cmd
}

func newCatalogReposCreateCmd() *cobra.Command {
	var name, url, repoType, description, authType string
	var enabled, isDefault bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Register a new Helm repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(url) == "" {
				return fmt.Errorf("--name and --url are required")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1CatalogRepositoriesJSONRequestBody{
				Name:        name,
				Url:         url,
				RepoType:    catStrPtr(repoType),
				Description: catStrPtr(description),
				AuthType:    catStrPtr(authType),
			}
			if cmd.Flags().Changed("enabled") {
				body.Enabled = &enabled
			}
			if cmd.Flags().Changed("default") {
				body.IsDefault = &isDefault
			}
			resp, err := client.PostApiV1CatalogRepositoriesWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			return render(cmd, resp.JSON201.Data, func(w io.Writer) error {
				return writeReposTable(w, []astroclient.HelmRepository{resp.JSON201.Data})
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "repository name (required)")
	cmd.Flags().StringVar(&url, "url", "", "repository URL (required)")
	cmd.Flags().StringVar(&repoType, "repo-type", "", "helm|oci (auto-detected for oci:// URLs)")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringVar(&authType, "auth-type", "", "auth type (e.g. basic, token)")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable the repository")
	cmd.Flags().BoolVar(&isDefault, "default", false, "mark as the default repository")
	// NOTE: --auth-config (raw JSON credentials map) is not exposed as a flag
	// here; pass credentials via the dashboard or a follow-up that accepts a
	// JSON file. Everything else in the create schema is wired.
	return cmd
}

func newCatalogReposUpdateCmd() *cobra.Command {
	var name, url, repoType, description, authType string
	var enabled, isDefault bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a Helm repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			body := astroclient.PutApiV1CatalogRepositoriesIdJSONRequestBody{
				Name:        catStrPtr(name),
				Url:         catStrPtr(url),
				RepoType:    catStrPtr(repoType),
				Description: catStrPtr(description),
				AuthType:    catStrPtr(authType),
			}
			if cmd.Flags().Changed("enabled") {
				body.Enabled = &enabled
			}
			if cmd.Flags().Changed("default") {
				body.IsDefault = &isDefault
			}
			resp, err := client.PutApiV1CatalogRepositoriesIdWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			return render(cmd, resp.JSON200.Data, func(w io.Writer) error {
				return writeReposTable(w, []astroclient.HelmRepository{resp.JSON200.Data})
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "repository name")
	cmd.Flags().StringVar(&url, "url", "", "repository URL")
	cmd.Flags().StringVar(&repoType, "repo-type", "", "helm|oci")
	cmd.Flags().StringVar(&description, "description", "", "free-form description")
	cmd.Flags().StringVar(&authType, "auth-type", "", "auth type")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable/disable the repository")
	cmd.Flags().BoolVar(&isDefault, "default", false, "mark as the default repository")
	return cmd
}

func newCatalogReposDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a Helm repository",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to delete repository %s. Proceed? [y/N] ", args[0]); err != nil {
					return err
				}
				var resp string
				_, _ = fmt.Scanln(&resp)
				resp = strings.ToLower(strings.TrimSpace(resp))
				if resp != "y" && resp != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			resp, err := client.DeleteApiV1CatalogRepositoriesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			// 204/200 success carries no body; any populated error envelope
			// means failure.
			if resp.JSON400 != nil || resp.JSON500 != nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON500)
			}
			if resp.StatusCode() >= 300 {
				return catalogError(resp.Status(), resp.StatusCode())
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted repository %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newCatalogRepoSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync <id>",
		Short: "Sync a repository's chart index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1CatalogRepositoriesIdSyncWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404, resp.JSON500, resp.JSON502)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

func newCatalogRepoTestConnectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "test-connection <id>",
		Aliases: []string{"test"},
		Short:   "Test connectivity to a repository",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1CatalogRepositoriesIdTestConnectionWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				// The 502 body shares the success/message shape; surface its
				// message if present.
				if resp.JSON502 != nil && resp.JSON502.Message != nil {
					return fmt.Errorf("connection failed: %s", *resp.JSON502.Message)
				}
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// installed
// ---------------------------------------------------------------------

func newCatalogInstalledCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "installed",
		Aliases: []string{"install", "installs"},
		Short:   "Manage installed charts",
	}
	cmd.AddCommand(
		newCatalogInstalledListCmd(),
		newCatalogInstalledGetValuesCmd(),
		newCatalogInstallCmd(),
		newCatalogUpgradeCmd(),
		newCatalogRollbackCmd(),
		newCatalogUninstallCmd(),
	)
	return cmd
}

func newCatalogInstalledListCmd() *cobra.Command {
	var clusterID string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed charts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogInstalledParams{}
			if strings.TrimSpace(clusterID) != "" {
				cid, err := catalogUUID(clusterID)
				if err != nil {
					return fmt.Errorf("invalid --cluster: %w", err)
				}
				params.ClusterId = &cid
			}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1CatalogInstalledWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON500)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				return writeInstalledTable(w, resp.JSON200.Data)
			})
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster", "", "filter by cluster id")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	return cmd
}

func newCatalogInstalledGetValuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-values <id>",
		Short: "Show an installed chart's values override",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogInstalledIdValuesWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON404)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				if resp.JSON200.ValuesOverride != nil {
					_, err := io.WriteString(w, *resp.JSON200.ValuesOverride)
					return err
				}
				_, err := fmt.Fprintln(w, "(no values override)")
				return err
			})
		},
	}
	return cmd
}

func newCatalogInstallCmd() *cobra.Command {
	var clusterID, namespace, releaseName, chartVersionID, valuesOverride, toolSlug, notes string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Install a chart (create an installed chart)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(clusterID) == "" || strings.TrimSpace(namespace) == "" || strings.TrimSpace(releaseName) == "" {
				return fmt.Errorf("--cluster, --namespace and --release-name are required")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			cid, err := catalogUUID(clusterID)
			if err != nil {
				return fmt.Errorf("invalid --cluster: %w", err)
			}
			body := astroclient.PostApiV1CatalogInstalledJSONRequestBody{
				ClusterId:      cid,
				Namespace:      namespace,
				ReleaseName:    releaseName,
				ValuesOverride: catStrPtr(valuesOverride),
				ToolSlug:       catStrPtr(toolSlug),
				Notes:          catStrPtr(notes),
			}
			if strings.TrimSpace(chartVersionID) != "" {
				cvid, err := catalogUUID(chartVersionID)
				if err != nil {
					return fmt.Errorf("invalid --chart-version: %w", err)
				}
				body.ChartVersionId = &cvid
			}
			resp, err := client.PostApiV1CatalogInstalledWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON409, resp.JSON500)
			}
			return renderInstallResult(cmd, resp.JSON202.Installation, resp.JSON202.Operation)
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster", "", "target cluster id (required)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "target namespace (required)")
	cmd.Flags().StringVar(&releaseName, "release-name", "", "Helm release name (required)")
	cmd.Flags().StringVar(&chartVersionID, "chart-version", "", "chart version id to install")
	cmd.Flags().StringVar(&valuesOverride, "values", "", "values override (raw YAML string)")
	cmd.Flags().StringVar(&toolSlug, "tool-slug", "", "tool slug")
	cmd.Flags().StringVar(&notes, "notes", "", "free-form notes")
	return cmd
}

func newCatalogUpgradeCmd() *cobra.Command {
	var valuesOverride string
	cmd := &cobra.Command{
		Use:   "upgrade <id>",
		Short: "Upgrade an installed chart",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			body := astroclient.PutApiV1CatalogInstalledIdUpgradeJSONRequestBody{
				ValuesOverride: catStrPtr(valuesOverride),
			}
			resp, err := client.PutApiV1CatalogInstalledIdUpgradeWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON500)
			}
			return renderInstallResult(cmd, resp.JSON202.Installation, resp.JSON202.Operation)
		},
	}
	cmd.Flags().StringVar(&valuesOverride, "values", "", "new values override (raw YAML string)")
	return cmd
}

func newCatalogRollbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback <id>",
		Short: "Roll back an installed chart to its previous revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1CatalogInstalledIdRollbackWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON500)
			}
			return render(cmd, resp.JSON202, func(w io.Writer) error {
				return writeOperationsTable(w, []astroclient.CatalogOperation{*resp.JSON202})
			})
		},
	}
	return cmd
}

func newCatalogUninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"uninstall", "rm"},
		Short:   "Uninstall an installed chart",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to uninstall %s. Proceed? [y/N] ", args[0]); err != nil {
					return err
				}
				var resp string
				_, _ = fmt.Scanln(&resp)
				resp = strings.ToLower(strings.TrimSpace(resp))
				if resp != "y" && resp != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			resp, err := client.DeleteApiV1CatalogInstalledIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON409, resp.JSON500)
			}
			return render(cmd, resp.JSON202, func(w io.Writer) error {
				return writeOperationsTable(w, []astroclient.CatalogOperation{*resp.JSON202})
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// renderInstallResult renders the {installation, operation} envelope returned
// by install/upgrade. The table form shows the queued operation; json/yaml
// keep the full envelope.
func renderInstallResult(cmd *cobra.Command, inst *astroclient.InstalledChart, op *astroclient.CatalogOperation) error {
	envelope := map[string]any{"installation": inst, "operation": op}
	return render(cmd, envelope, func(w io.Writer) error {
		if op != nil {
			return writeOperationsTable(w, []astroclient.CatalogOperation{*op})
		}
		if inst != nil {
			return writeInstalledTable(w, []astroclient.InstalledChart{*inst})
		}
		_, err := fmt.Fprintln(w, "(no result)")
		return err
	})
}

// ---------------------------------------------------------------------
// operations
// ---------------------------------------------------------------------

func newCatalogOperationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "operations",
		Aliases: []string{"operation", "ops", "op"},
		Short:   "Inspect catalog operations",
	}
	cmd.AddCommand(
		newCatalogOpsListCmd(),
		newCatalogOpsGetCmd(),
		newCatalogOpRetryCmd(),
	)
	return cmd
}

func newCatalogOpsListCmd() *cobra.Command {
	var targetType, targetKey, status string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog operations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1CatalogOperationsParams{
				TargetType: catStrPtr(targetType),
				TargetKey:  catStrPtr(targetKey),
				Status:     catStrPtr(status),
			}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1CatalogOperationsWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON500)
			}
			return render(cmd, resp.JSON200, func(w io.Writer) error {
				return writeOperationsTable(w, *resp.JSON200)
			})
		},
	}
	cmd.Flags().StringVar(&targetType, "target-type", "", "filter by target type")
	cmd.Flags().StringVar(&targetKey, "target-key", "", "filter by target key")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	return cmd
}

func newCatalogOpsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one operation's details (including events)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogOperationsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON500)
			}
			// The detail body is an anonymous struct (with events); just defer
			// to the generic renderer so all fields show in json/yaml and a
			// KV table by default.
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

func newCatalogOpRetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry a failed operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := catalogUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1CatalogOperationsIdRetryWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON400, resp.JSON403, resp.JSON404, resp.JSON409, resp.JSON500)
			}
			return render(cmd, resp.JSON202, func(w io.Writer) error {
				return writeOperationsTable(w, []astroclient.CatalogOperation{*resp.JSON202})
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// controller-status
// ---------------------------------------------------------------------

func newCatalogControllerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "controller-status",
		Aliases: []string{"status"},
		Short:   "Show the catalog controller status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1CatalogControllerStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return catalogError(resp.Status(), resp.StatusCode(), resp.JSON500)
			}
			// The status body is a nested anonymous struct; let renderSDK do a
			// best-effort KV table, with json/yaml as the lossless views.
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Table writers
// ---------------------------------------------------------------------

func writeChartsTable(w io.Writer, rows []astroclient.HelmChart) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no charts)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tDISPLAY\tCATEGORY\tDEPRECATED"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			catUUIDShort(r.Id), catStr(r.Name), catStr(r.DisplayName),
			catStrOr(r.Category, "—"), catBool(r.Deprecated)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeChartVersionsTable(w io.Writer, rows []astroclient.HelmChartVersion) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no versions)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "VERSION\tAPP_VERSION\tDIGEST\tID"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			catStrOr(r.Version, "—"), catStrOr(r.AppVersion, "—"),
			truncate(catStr(r.Digest), 16), catUUIDShort(r.Id)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeReposTable(w io.Writer, rows []astroclient.HelmRepository) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no repositories)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tTYPE\tURL\tENABLED\tDEFAULT"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			catUUIDShort(r.Id), catStr(r.Name), catStrOr(r.RepoType, "—"),
			catStr(r.Url), catBool(r.Enabled), catBool(r.IsDefault)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeInstalledTable(w io.Writer, rows []astroclient.InstalledChart) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no installed charts)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tRELEASE\tNAMESPACE\tSTATUS\tREVISION\tCLUSTER"); err != nil {
		return err
	}
	for _, r := range rows {
		rev := "—"
		if r.Revision != nil {
			rev = fmt.Sprintf("%d", *r.Revision)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			catUUIDShort(r.Id), catStr(r.ReleaseName), catStrOr(r.Namespace, "—"),
			catStrOr(r.Status, "—"), rev, catUUIDShort(r.ClusterId)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeOperationsTable(w io.Writer, rows []astroclient.CatalogOperation) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no operations)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tTYPE\tSTATUS\tTARGET\tATTEMPTS\tERROR"); err != nil {
		return err
	}
	for _, r := range rows {
		att := "—"
		if r.AttemptCount != nil {
			att = fmt.Sprintf("%d", *r.AttemptCount)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			catUUIDShort(r.Id), catStrOr(r.OperationType, "—"), catStrOr(r.Status, "—"),
			catStrOr(r.TargetKey, "—"), att, truncate(catStr(r.ErrorMessage), 40)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// ---------------------------------------------------------------------
// Small deref helpers
// ---------------------------------------------------------------------

func catStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func catStrOr(s *string, fallback string) string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return fallback
	}
	return *s
}

func catBool(b *bool) string {
	if b == nil {
		return "—"
	}
	if *b {
		return "yes"
	}
	return "no"
}

func catUUIDShort(id *openapi_types.UUID) string {
	if id == nil {
		return "—"
	}
	return truncate(id.String(), 8)
}
