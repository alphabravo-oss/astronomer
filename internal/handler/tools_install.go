package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"
)

// errToolNotFound is returned by resolveAction when the slug doesn't match any
// tool. Callers translate this into a 404 with a clean "Tool not found" body
// rather than leaking pgx's "no rows in result set" string to the API client.
var errToolNotFound = errors.New("tool not found")

// resolveAction consumes the request body before the maintenance gate runs.
// Restore a canonical copy so defer mode captures a complete, replayable
// request envelope rather than an empty body.
func restoreToolActionRequestBody(r *http.Request, req toolActionRequest) {
	if r == nil {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
}

func (h *ToolHandler) sendHelmRaw(ctx context.Context, env toolReleaseExecution, msgType protocol.MessageType) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, errors.New("helm requester not configured")
	}
	// Resolve ${vault://...} markers at execution time so the resolved
	// plaintext never lands in tool_operations.payload or the
	// installed_charts row — it only exists in-memory on the wire.
	blob, err := vaultResolveBlob(ctx, h.vaultResolver, uuid.Nil, env.ValuesYAML)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if blob != "" {
		if err := yaml.Unmarshal([]byte(blob), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, env.ClusterID, msgType, protocol.HelmRequestPayload{
		Description: env.Description,
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
		ChartName:   env.ChartName,
		RepoURL:     env.RepoURL,
		Version:     env.Version,
		Values:      values,
	})
}

var errInstalledChartNotFound = errors.New("installed chart not found")

func (h *ToolHandler) findInstalledTool(ctx context.Context, clusterID uuid.UUID, slug string) (sqlc.InstalledChart, error) {
	// Indexed (cluster_id, tool_slug) lookup. The previous first-200-row
	// in-Go scan missed the duplicate-install 409 once a cluster had more
	// than 200 installed charts before the one being re-installed.
	item, err := h.queries.GetInstalledChartByClusterAndTool(ctx, sqlc.GetInstalledChartByClusterAndToolParams{
		ClusterID: clusterID,
		ToolSlug:  slug,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.InstalledChart{}, errInstalledChartNotFound
		}
		return sqlc.InstalledChart{}, err
	}
	return item, nil
}

func parseToolCharts(raw json.RawMessage) ([]toolChart, error) {
	var charts []toolChart
	err := json.Unmarshal(raw, &charts)
	return charts, err
}

func firstChart(charts []toolChart) toolChart {
	if len(charts) == 0 {
		return toolChart{}
	}
	best := charts[0]
	for _, chart := range charts[1:] {
		if chart.Order < best.Order {
			best = chart
		}
	}
	return best
}

func chartNamespace(tool sqlc.ClusterTool, chart toolChart) string {
	if chart.Namespace != "" {
		return chart.Namespace
	}
	return tool.DefaultNamespace
}

func presetValuesYAML(raw json.RawMessage, preset string) string {
	if preset == "" {
		return ""
	}
	var presets map[string]any
	if json.Unmarshal(raw, &presets) != nil {
		return ""
	}
	value, ok := presets[preset]
	if !ok {
		return ""
	}
	// A preset can be stored either as a raw YAML string (the seed-
	// migration style — easier to author) or as a nested map (when
	// operators edit it through the UI which posts structured JSON).
	// yaml.Marshal'ing a string wraps it in quoted-string syntax which
	// helm refuses ("cannot unmarshal string into map"), so pass string
	// values through unchanged.
	if s, isString := value.(string); isString {
		return s
	}
	data, _ := yaml.Marshal(value)
	return string(data)
}

// mergeValueLayers deep-merges YAML values layers in increasing precedence
// (later layers win on key conflicts) and returns a single values document.
// It is used to combine the distribution base overrides, the preset, and the
// operator's values_override without the "duplicate top-level key drops the
// earlier one" hazard of string-concatenating YAML documents: sibling keys
// under a shared parent (e.g. securityContext.privileged vs
// securityContext.fsGroup) are all preserved. If any layer cannot be parsed
// as a mapping, it falls back to document concatenation (last-wins), matching
// the previous behaviour.
func mergeValueLayers(layers ...string) string {
	merged := map[string]any{}
	for _, layer := range layers {
		if strings.TrimSpace(layer) == "" {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(layer), &m); err != nil || m == nil {
			return concatValueLayers(layers...)
		}
		merged = mergeValueMaps(merged, m)
	}
	out, err := yaml.Marshal(merged)
	if err != nil {
		return concatValueLayers(layers...)
	}
	return string(out)
}

// mergeValueMaps recursively merges override into base, returning a new map.
// Nested maps are merged key-by-key; every other type (scalars, sequences) is
// replaced wholesale by the override, so an operator list fully overrides a
// base list rather than being concatenated.
func mergeValueMaps(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if bv, ok := out[k]; ok {
			if bm, bok := bv.(map[string]any); bok {
				if om, ook := v.(map[string]any); ook {
					out[k] = mergeValueMaps(bm, om)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

// concatValueLayers is the fallback for mergeValueLayers when a layer is not a
// YAML mapping: it joins the non-empty layers with newlines in precedence
// order (later layers last), reproducing the historical concatenation.
func concatValueLayers(layers ...string) string {
	parts := make([]string, 0, len(layers))
	for _, l := range layers {
		if strings.TrimSpace(l) != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, "\n")
}

func normalizeToolStatus(status string) string {
	switch status {
	case "deployed":
		return "installed"
	case "pending", "pending_install", "pending-install":
		return "installing"
	case "pending_upgrade", "pending-upgrade":
		return "upgrading"
	case "pending_uninstall", "pending-uninstall":
		return "uninstalling"
	default:
		return status
	}
}

// checkToolMaintenanceWindow consults the migration-057 maintenance
// gate and writes the 409/202 response when the operation is blocked.
// Returns true when the caller should stop processing (response
// already written). The cluster lookup tolerates errors silently —
// failing the gate on a missing cluster row would mask the bigger
// problem of the cluster being gone, so we let the underlying handler
// emit its own not-found.
func (h *ToolHandler) checkToolMaintenanceWindow(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, opType string) bool {
	if h == nil || h.maintenanceGate == nil {
		return false
	}
	labels := map[string]string{}
	if cluster, err := h.queries.GetClusterByID(r.Context(), clusterID); err == nil {
		labels = MaintenanceGateClusterLabels(cluster)
	}
	return EnforceMaintenanceWindow(w, r, h.maintenanceGate, opType, labels,
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{})
}

type toolInstallPersister interface {
	GetInstalledChartByRelease(ctx context.Context, arg sqlc.GetInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	AdoptInstalledChartByRelease(ctx context.Context, arg sqlc.AdoptInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
}

// SetEventBus wires the SSE bus for tool_operation.changed liveness events
// (P4.5). Optional: publishers are fire-and-forget and nil-safe.
func (h *ToolHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishToolOperationChanged emits the metadata-only tool_operation.changed
// event after a successful operation-row write. The cluster id comes from
// the persisted payload envelope (best-effort: rows without one publish
// unscoped and are superuser-only via the SEC-R07 fail-closed drop).
func (h *ToolHandler) publishToolOperationChanged(op sqlc.ToolOperation) {
	if h == nil {
		return
	}
	var env toolOperationEnvelope
	_ = json.Unmarshal(op.Payload, &env)
	events.PublishChanged(h.bus, "tool_operation", env.ClusterID, op.ID.String(), map[string]any{"status": op.Status})
}
