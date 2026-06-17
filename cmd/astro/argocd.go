// ArgoCD subcommands: instances list / orphan-report / ui (proxy
// passthrough) / proxy (internal k8s proxy passthrough).
//
// These talk to the generated pkg/astroclient SDK via the shared
// newAstroClient foundation helper (client.go) and render results
// honoring the global -o/--output flag through renderSDK / render
// (output.go). Style mirrors cluster.go: one constructor per
// subcommand, grouped under a parent "argocd" command.
//
// Naming note: the originating task referred to this constructor with a
// path-mangled name ("newcmd/astro/argocd.go (optional 13th — proxy
// passthrough group)Cmd"), which is not a valid Go identifier. It is
// implemented here as newArgocdCmd; the later root-wiring integration
// step should reference this name.
//
// All package-private helpers in this file are prefixed "argocd" so they
// never collide with identically-purposed helpers other command groups
// define in sibling files of the same `main` package.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newArgocdCmd is the parent for all ArgoCD-related subcommands.
func newArgocdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "argocd",
		Aliases: []string{"argo"},
		Short:   "Inspect ArgoCD instances and proxy to the ArgoCD UI / k8s API",
		Long: `Inspect the platform's registered ArgoCD instances and their
orphaned-application reports, and proxy raw requests through to the
ArgoCD UI or a cluster's internal Kubernetes API.

  argocd instances list                 list registered ArgoCD instances
  argocd orphan-report <instance-id>    orphaned-application report for one instance
  argocd ui <path...>                   GET (or --post) through the ArgoCD UI proxy
  argocd proxy <cluster-id> <path...>   GET through the internal k8s API proxy`,
	}
	cmd.AddCommand(
		newArgocdInstancesCmd(),
		newArgocdOrphanReportCmd(),
		newArgocdUICmd(),
		newArgocdProxyCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Helpers (argocd-prefixed to avoid collisions with sibling groups)
// ---------------------------------------------------------------------

// argocdParseUUID converts a user-supplied id argument into the UUID type
// the SDK expects, returning a clean error on bad input.
func argocdParseUUID(label, arg string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(arg))
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid %s %q: must be a UUID", label, arg)
	}
	return id, nil
}

// argocdStatusErr turns an unexpected (non-2xx, or missing typed body)
// SDK response into a clean, action-prefixed error suitable for surfacing
// to the operator with a non-zero exit. The HTTP status and any response
// body are included to aid debugging.
func argocdStatusErr(action string, httpResp *http.Response, body []byte) error {
	status := 0
	if httpResp != nil {
		status = httpResp.StatusCode
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("%s: unexpected response (HTTP %d)", action, status)
	}
	// Keep the surfaced body compact; json/yaml output is the lossless path.
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	return fmt.Errorf("%s: HTTP %d: %s", action, status, msg)
}

// argocdJoinPath normalizes positional path args ("apps", "v1", ...) into
// a single slash-joined path with no leading slash, matching what the SDK
// proxy methods expect for their {path} segment.
func argocdJoinPath(parts []string) string {
	return strings.TrimPrefix(strings.Join(parts, "/"), "/")
}

// ---------------------------------------------------------------------
// instances list
// ---------------------------------------------------------------------

func newArgocdInstancesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instances",
		Short: "Manage ArgoCD instances",
	}
	cmd.AddCommand(newArgocdInstancesListCmd())
	return cmd
}

func newArgocdInstancesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered ArgoCD instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1ArgocdInstancesWithResponse(cmd.Context())
			if err != nil {
				return fmt.Errorf("list argocd instances: %w", err)
			}
			if resp.JSON200 == nil {
				return argocdStatusErr("list argocd instances", resp.HTTPResponse, resp.Body)
			}
			// The generated body is a free-form map[string]interface{}
			// envelope (no typed Data slice). Render it generically and
			// honor -o/--output; surface the inner "data" list as rows
			// when present so the default table is useful.
			body := *resp.JSON200
			if data, ok := body["data"]; ok {
				return renderSDK(cmd, data)
			}
			return renderSDK(cmd, body)
		},
	}
}

// ---------------------------------------------------------------------
// orphan-report <instance-id>
// ---------------------------------------------------------------------

func newArgocdOrphanReportCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "orphan-report <instance-id>",
		Short: "Show the orphaned-application report for an ArgoCD instance",
		Long: `Reports applications ArgoCD is tracking that no longer map to a
managed target (orphans), plus live/cached application counts for the
instance. Use --limit to cap the number of orphan applications listed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := argocdParseUUID("instance id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			var params *astroclient.GetApiV1ArgocdInstancesIdOrphanReportParams
			if cmd.Flags().Changed("limit") {
				l := limit
				params = &astroclient.GetApiV1ArgocdInstancesIdOrphanReportParams{Limit: &l}
			}
			resp, err := client.GetApiV1ArgocdInstancesIdOrphanReportWithResponse(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("orphan report: %w", err)
			}
			if resp.JSON200 == nil {
				return argocdStatusErr("orphan report", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max orphan applications to include")
	return cmd
}

// ---------------------------------------------------------------------
// ui <path...> (proxy passthrough)
// ---------------------------------------------------------------------

func newArgocdUICmd() *cobra.Command {
	var post bool
	var bodyJSON string
	cmd := &cobra.Command{
		Use:   "ui [path...]",
		Short: "Proxy a request through to the ArgoCD UI",
		Long: `Proxies a request through the platform's ArgoCD UI passthrough
endpoint and prints the raw response body, honoring -o/--output for the
JSON envelope.

By default a GET is issued. Pass --post to issue a POST; --body supplies
the (JSON) request payload for the POST.

Path-passthrough note: the generated SDK's ArgocdUiGet/ArgocdUiPost
methods target the ArgoCD UI root and do NOT accept a per-request path
segment, so any positional path arguments are ignored here. Deep-path
passthrough would require the ArgocdUiSubGet/ArgocdUiSubPost variants;
see the gap note in the command Report.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}

			if post {
				var payload interface{}
				if strings.TrimSpace(bodyJSON) != "" {
					if err := json.Unmarshal([]byte(bodyJSON), &payload); err != nil {
						return fmt.Errorf("invalid --body JSON: %w", err)
					}
				}
				resp, err := client.ArgocdUiPostWithResponse(cmd.Context(), payload)
				if err != nil {
					return fmt.Errorf("argocd ui post: %w", err)
				}
				if resp.JSON200 == nil {
					return argocdStatusErr("argocd ui post", resp.HTTPResponse, resp.Body)
				}
				return renderSDK(cmd, *resp.JSON200)
			}

			resp, err := client.ArgocdUiGetWithResponse(cmd.Context())
			if err != nil {
				return fmt.Errorf("argocd ui get: %w", err)
			}
			if resp.JSON200 == nil {
				return argocdStatusErr("argocd ui get", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
	cmd.Flags().BoolVar(&post, "post", false, "issue a POST instead of a GET")
	cmd.Flags().StringVar(&bodyJSON, "body", "", "JSON request body for --post")
	return cmd
}

// ---------------------------------------------------------------------
// proxy <cluster-id> <path...> (internal k8s proxy passthrough)
// ---------------------------------------------------------------------

func newArgocdProxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy <cluster-id> <path...>",
		Short: "GET through a cluster's internal Kubernetes API proxy",
		Long: `Proxies a GET request through to a cluster's internal Kubernetes
API server (the path used by the ArgoCD integration) and prints the raw
response, honoring -o/--output.

The first argument is the cluster UUID; the remaining arguments are
joined into the request path (e.g. "api v1 namespaces" -> "api/v1/namespaces").`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := argocdParseUUID("cluster id", args[0])
			if err != nil {
				return err
			}
			path := argocdJoinPath(args[1:])
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.ArgocdInternalK8sProxyGetWithResponse(cmd.Context(), clusterID, path)
			if err != nil {
				return fmt.Errorf("argocd proxy: %w", err)
			}
			if resp.JSON200 == nil {
				return argocdStatusErr("argocd proxy", resp.HTTPResponse, resp.Body)
			}
			return renderSDK(cmd, *resp.JSON200)
		},
	}
}
