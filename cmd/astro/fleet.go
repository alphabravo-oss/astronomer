// Fleet subcommands: agent-fleet listing, fleet operations (list/get/targets
// + abort/pause/resume/retry-failed/trigger), and per-cluster lifecycle
// (self-test / upgrade / upgrade-plan / operations / diagnostics).
//
// This group talks to the generated pkg/astroclient SDK via the shared
// foundation helpers in client.go (newAstroClient + renderSDK). Each
// subcommand builds the typed client, calls one SDK method, maps a
// non-2xx HTTP status (or transport error) to a clean message + non-zero
// exit (reusing the package-level sdkError / parseUUID / pageParam
// helpers), and renders the typed JSON body honoring -o/--output.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func newFleetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "fleet",
		Aliases: []string{"agents-fleet"},
		Short:   "Inspect the agent fleet and drive fleet/cluster lifecycle operations",
	}
	cmd.AddCommand(
		newFleetListCmd(),
		newFleetOperationsCmd(),
		newFleetTriggerCmd(),
		newFleetSelfTestCmd(),
		newFleetUpgradeCmd(),
		newFleetUpgradePlanCmd(),
		newFleetDiagnosticsCmd(),
		newFleetDiagnosticsBundleCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// list — GET /api/v1/agents/fleet
// ---------------------------------------------------------------------

func newFleetListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the agent fleet (one row per cluster's agent)",
		Args:  cobra.NoArgs,
	}
	limit, offset := fleetLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1AgentsFleetParams{
			Limit:  fleetPageParam(cmd, "limit", limit),
			Offset: fleetPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1AgentsFleetWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

// ---------------------------------------------------------------------
// operations — fleet-wide operation queue
// ---------------------------------------------------------------------

func newFleetOperationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "operations",
		Aliases: []string{"ops", "operation"},
		Short:   "List, inspect and control fleet operations",
	}
	cmd.AddCommand(
		newFleetOperationsListCmd(),
		newFleetOperationsGetCmd(),
		newFleetOperationsTargetsCmd(),
		newFleetOperationsActionCmd("abort", "Abort a running fleet operation"),
		newFleetOperationsActionCmd("pause", "Pause a running fleet operation"),
		newFleetOperationsActionCmd("resume", "Resume a paused fleet operation"),
		newFleetOperationsActionCmd("retry-failed", "Retry the failed targets of a fleet operation"),
	)
	return cmd
}

func newFleetOperationsListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List fleet operations",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by operation status")
	limit, offset := fleetLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1FleetOperationsParams{
			Limit:  fleetPageParam(cmd, "limit", limit),
			Offset: fleetPageParam(cmd, "offset", offset),
		}
		if cmd.Flags().Changed("status") {
			params.Status = &status
		}
		resp, err := client.GetApiV1FleetOperationsWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
		}
		return renderSDK(cmd, anyJSON(resp.JSON200.Data))
	}
	return cmd
}

func newFleetOperationsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one fleet operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1FleetOperationsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return fleetSDKError(resp.HTTPResponse, resp.JSON404, resp.Body)
			}
			return renderSDK(cmd, anyJSON(resp.JSON200.Data))
		},
	}
	return cmd
}

func newFleetOperationsTargetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "targets <id>",
		Short: "List the per-cluster targets of a fleet operation",
		Args:  cobra.ExactArgs(1),
	}
	limit, offset := fleetLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseUUID("id", args[0])
		if err != nil {
			return err
		}
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1FleetOperationsIdTargetsParams{
			Limit:  fleetPageParam(cmd, "limit", limit),
			Offset: fleetPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1FleetOperationsIdTargetsWithResponse(cmd.Context(), id, params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
		}
		return renderSDK(cmd, anyJSON(resp.JSON200.Data))
	}
	return cmd
}

// newFleetOperationsActionCmd builds one of the state-transition POSTs
// (abort/pause/resume/retry-failed). They share an identical shape:
// POST /api/v1/fleet/operations/{id}/<action> -> 202 { data: operation }
// with a 404 ErrorEnvelope on a missing/ineligible operation.
func newFleetOperationsActionCmd(action, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}

			var (
				data    any
				bodyErr error
			)
			switch action {
			case "abort":
				r, err := client.PostApiV1FleetOperationsIdAbortWithResponse(cmd.Context(), id)
				if err != nil {
					return err
				}
				if r.JSON202 != nil {
					data = anyJSON(r.JSON202.Data)
				} else {
					bodyErr = fleetSDKError(r.HTTPResponse, r.JSON404, r.Body)
				}
			case "pause":
				r, err := client.PostApiV1FleetOperationsIdPauseWithResponse(cmd.Context(), id)
				if err != nil {
					return err
				}
				if r.JSON202 != nil {
					data = anyJSON(r.JSON202.Data)
				} else {
					bodyErr = fleetSDKError(r.HTTPResponse, r.JSON404, r.Body)
				}
			case "resume":
				r, err := client.PostApiV1FleetOperationsIdResumeWithResponse(cmd.Context(), id)
				if err != nil {
					return err
				}
				if r.JSON202 != nil {
					data = anyJSON(r.JSON202.Data)
				} else {
					bodyErr = fleetSDKError(r.HTTPResponse, r.JSON404, r.Body)
				}
			case "retry-failed":
				r, err := client.PostApiV1FleetOperationsIdRetryFailedWithResponse(cmd.Context(), id)
				if err != nil {
					return err
				}
				if r.JSON202 != nil {
					data = anyJSON(r.JSON202.Data)
				} else {
					bodyErr = fleetSDKError(r.HTTPResponse, r.JSON404, r.Body)
				}
			default:
				return fmt.Errorf("unknown operation action %q", action)
			}

			if data == nil {
				return bodyErr
			}
			return renderSDK(cmd, data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// trigger — POST /api/v1/fleet/operations (create operation)
// ---------------------------------------------------------------------

func newFleetTriggerCmd() *cobra.Command {
	var bodyFlag string
	cmd := &cobra.Command{
		Use:   "trigger",
		Short: "Create (trigger) a fleet operation",
		Long: `Create a fleet operation. The request body is the handler's JSON
payload (the spec ships it as a permissive object), so pass it verbatim:

  astro fleet trigger --body '{"operation_type":"upgrade","cluster_ids":["..."]}'
  astro fleet trigger --body @operation.json   # read JSON from a file
  astro fleet trigger --body @-                 # read JSON from stdin

The created operation is printed; control it with
"astro fleet operations get|pause|resume|abort|retry-failed <id>".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readBodyFlag(cmd, bodyFlag)
			if err != nil {
				return err
			}
			var payload astroclient.CreateFleetOperationRequest
			if err := json.Unmarshal(raw, &payload); err != nil {
				return fmt.Errorf("--body must be a JSON object: %w", err)
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1FleetOperationsWithResponse(cmd.Context(), payload)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return fleetSDKError(resp.HTTPResponse, resp.JSON400, resp.Body)
			}
			return renderSDK(cmd, anyJSON(resp.JSON201.Data))
		},
	}
	cmd.Flags().StringVar(&bodyFlag, "body", "", "operation spec as inline JSON, or @file (@- for stdin)")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}

// ---------------------------------------------------------------------
// per-cluster lifecycle
// ---------------------------------------------------------------------

func newFleetSelfTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-test <cluster-id>",
		Short: "Run agent health checks for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AgentsFleetClusterIdSelfTestWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newFleetUpgradeCmd() *cobra.Command {
	var p upgradePlanFlags
	cmd := &cobra.Command{
		Use:   "upgrade <cluster-id>",
		Short: "Start an agent upgrade for a cluster",
		Long: `Start an agent upgrade. Supply the target via flags (e.g.
--target-version or --target-image) plus optional rollout controls.
Returns the created operation + the resolved plan.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AgentsFleetClusterIdUpgradeWithResponse(cmd.Context(), id, p.toRequest(cmd))
			if err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
			}
			return renderSDK(cmd, resp.JSON202.Data)
		},
	}
	p.bind(cmd)
	return cmd
}

func newFleetUpgradePlanCmd() *cobra.Command {
	var p upgradePlanFlags
	cmd := &cobra.Command{
		Use:   "upgrade-plan <cluster-id>",
		Short: "Preview (dry-run) an agent upgrade plan for a cluster",
		Long: `Compute the upgrade plan without starting it: current vs target
image/version, preflight checks, blockers, and rollout sizing. Accepts the
same target/rollout flags as "astro fleet upgrade".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AgentsFleetClusterIdUpgradePlanWithResponse(cmd.Context(), id, p.toRequest(cmd))
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	p.bind(cmd)
	return cmd
}

func newFleetDiagnosticsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnostics <cluster-id>",
		Short: "Show agent diagnostics for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AgentsFleetClusterIdDiagnosticsWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				// JSON404 is the bare Error shape (not ErrorEnvelope); the
				// raw body already carries the message, so pass it through.
				return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newFleetDiagnosticsBundleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnostics-bundle <cluster-id>",
		Short: "Show the full agent diagnostics bundle for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("id", args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1AgentsFleetClusterIdDiagnosticsBundleWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return fleetSDKError(resp.HTTPResponse, nil, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// shared flag set for upgrade / upgrade-plan
// ---------------------------------------------------------------------

type upgradePlanFlags struct {
	targetVersion  string
	targetImage    string
	strategy       string
	rollbackImage  string
	batchSize      int
	maxUnavailable int
	canaryIDs      []string
}

func (p *upgradePlanFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.targetVersion, "target-version", "", "target agent version")
	cmd.Flags().StringVar(&p.targetImage, "target-image", "", "target agent image")
	cmd.Flags().StringVar(&p.strategy, "strategy", "", "rollout strategy")
	cmd.Flags().StringVar(&p.rollbackImage, "rollback-image", "", "image to roll back to on failure")
	cmd.Flags().IntVar(&p.batchSize, "batch-size", 0, "number of targets to upgrade per batch")
	cmd.Flags().IntVar(&p.maxUnavailable, "max-unavailable", 0, "max targets unavailable during rollout")
	cmd.Flags().StringSliceVar(&p.canaryIDs, "canary-cluster-id", nil, "cluster IDs to upgrade first (repeatable)")
}

// toRequest builds the AgentUpgradePlanRequest from only the flags the
// user actually set, so unset fields stay omitted (nil) and the server
// applies its defaults.
func (p *upgradePlanFlags) toRequest(cmd *cobra.Command) astroclient.AgentUpgradePlanRequest {
	body := astroclient.AgentUpgradePlanRequest{}
	if cmd.Flags().Changed("target-version") {
		body.TargetVersion = &p.targetVersion
	}
	if cmd.Flags().Changed("target-image") {
		body.TargetImage = &p.targetImage
	}
	if cmd.Flags().Changed("strategy") {
		body.Strategy = &p.strategy
	}
	if cmd.Flags().Changed("rollback-image") {
		body.RollbackImage = &p.rollbackImage
	}
	if cmd.Flags().Changed("batch-size") {
		body.BatchSize = &p.batchSize
	}
	if cmd.Flags().Changed("max-unavailable") {
		body.MaxUnavailable = &p.maxUnavailable
	}
	if cmd.Flags().Changed("canary-cluster-id") {
		ids := append([]string(nil), p.canaryIDs...)
		body.CanaryClusterIds = &ids
	}
	return body
}

// ---------------------------------------------------------------------
// helpers (fleet-prefixed to avoid colliding with the sibling command
// groups in package main; parseUUID lives in rbac.go and takes a label)
// ---------------------------------------------------------------------

// fleetLimitOffsetFlags registers --limit/--offset on cmd and returns the
// bound destinations.
func fleetLimitOffsetFlags(cmd *cobra.Command) (limit, offset *int) {
	limit = cmd.Flags().Int("limit", 0, "max items to return")
	offset = cmd.Flags().Int("offset", 0, "items to skip")
	return limit, offset
}

// fleetPageParam returns the flag value pointer only when the flag was set,
// so an unset --limit/--offset is omitted from the query string entirely.
func fleetPageParam(cmd *cobra.Command, name string, v *int) *int {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return v
	}
	return nil
}

// fleetSDKError builds a clean error for a non-2xx (or missing-body) SDK
// response, preferring the typed ErrorEnvelope when present. main.go prints
// it and exits non-zero (the root command sets SilenceErrors).
func fleetSDKError(httpResp *http.Response, env *astroclient.ErrorEnvelope, body []byte) error {
	status := 0
	if httpResp != nil {
		status = httpResp.StatusCode
	}
	if env != nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("request failed (HTTP %d): %s (%s)", status, env.Error.Message, env.Error.Code)
		}
		return fmt.Errorf("request failed (HTTP %d): %s", status, env.Error.Message)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("request failed (HTTP %d): %s", status, msg)
}

// anyJSON re-marshals a permissive/opaque SDK payload (the spec models
// several fleet-operation bodies as map[string]interface{}) into a plain
// any tree so renderSDK's generic table path can key off it. For typed
// structs callers pass the struct straight through instead.
func anyJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

// readBodyFlag resolves a --body value: literal JSON, or "@path" to read
// the JSON from a file ("@-" reads stdin).
func readBodyFlag(cmd *cobra.Command, val string) ([]byte, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, fmt.Errorf("--body is required")
	}
	if !strings.HasPrefix(val, "@") {
		return []byte(val), nil
	}
	path := strings.TrimPrefix(val, "@")
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
}
