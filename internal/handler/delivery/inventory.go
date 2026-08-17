package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/compatibility"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const fleetStaleAfter = 5 * time.Minute

type InventoryQueries interface {
	GetDeliveryControllerInventory(context.Context, sqlc.GetDeliveryControllerInventoryParams) (sqlc.DeliveryControllerInventory, error)
	ListClusterDeployments(context.Context, sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error)
	CountClusterDeployments(context.Context, sqlc.CountClusterDeploymentsParams) (int64, error)
	CountDeliveryControllerCompatibility(context.Context) ([]sqlc.CountDeliveryControllerCompatibilityRow, error)
	GetCurrentDeliverySystemRollout(context.Context) (sqlc.DeliverySystemRollout, error)
	ListDeliverySystemReleases(context.Context, sqlc.ListDeliverySystemReleasesParams) ([]sqlc.ListDeliverySystemReleasesRow, error)
	ListDeliveryFleetClusters(context.Context) ([]sqlc.ListDeliveryFleetClustersRow, error)
	CountActiveDeliveryRollouts(context.Context) (int64, error)
}

type InventoryHandler struct {
	queries InventoryQueries
	now     func() time.Time
}

func NewInventoryHandler(queries InventoryQueries) *InventoryHandler {
	return &InventoryHandler{queries: queries, now: time.Now}
}

func (h *InventoryHandler) Cluster(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	clusterID, err := pathUUID(r, "clusterId", "clusterID", "cluster_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery inventory persistence is unavailable")
		return
	}
	inventory, err := h.queries.GetDeliveryControllerInventory(r.Context(), sqlc.GetDeliveryControllerInventoryParams{ClusterID: clusterID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	cluster := pgtype.UUID{Bytes: clusterID, Valid: true}
	deployments, err := h.queries.ListClusterDeployments(r.Context(), sqlc.ListClusterDeploymentsParams{ProjectID: projectID, ClusterID: cluster, QueryLimit: maxPageLimit, QueryOffset: 0})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountClusterDeployments(r.Context(), sqlc.CountClusterDeploymentsParams{ProjectID: projectID, ClusterID: cluster})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondData(w, http.StatusOK, map[string]any{"controller_inventory": inventory, "deployments": deployments, "deployment_count": total})
}

func (h *InventoryHandler) SystemCompatibility(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery inventory persistence is unavailable")
		return
	}
	counts, err := h.queries.CountDeliveryControllerCompatibility(r.Context())
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	releases, err := h.queries.ListDeliverySystemReleases(r.Context(), sqlc.ListDeliverySystemReleasesParams{QueryLimit: 20})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	var currentRelease any
	for _, release := range releases {
		if release.State == "released" {
			currentRelease = release
			break
		}
	}
	var currentRollout any
	rollout, err := h.queries.GetCurrentDeliverySystemRollout(r.Context())
	if err == nil {
		currentRollout = map[string]any{
			"id": rollout.ID, "release_id": rollout.ReleaseID, "previous_release_id": rollout.PreviousReleaseID,
			"strategy": rollout.Strategy, "strategy_digest": rollout.StrategyDigest, "state": rollout.State,
			"fencing_generation": rollout.FencingGeneration, "total_clusters": rollout.TotalClusters,
			"ready_clusters": rollout.ReadyClusters, "failed_clusters": rollout.FailedClusters,
			"released_clusters": rollout.ReleasedClusters, "progress_deadline": rollout.ProgressDeadline,
			"started_at": rollout.StartedAt, "completed_at": rollout.CompletedAt,
			"last_error_code": rollout.LastErrorCode, "created_at": rollout.CreatedAt, "updated_at": rollout.UpdatedAt,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		respondDatabaseError(w, err)
		return
	}
	respondData(w, http.StatusOK, map[string]any{
		"contract": map[string]any{
			"summary": compatibility.ContractSummary(), "flux_version": fluxdistribution.Version(),
			"flux_components": compatibility.RequiredComponentVersions(), "flux_apis": compatibility.RequiredAPIs(),
			"kubernetes_minimum": "1.33", "kubernetes_maximum": "1.35",
			"agent_protocol":        protocol.DeliveryProtocolVersion,
			"required_capabilities": []string{protocol.FeatureDeliveryAssignmentsV2, protocol.FeatureDeliveryStatusV2, protocol.FeatureDeliverySystemV2},
		},
		"current_release": currentRelease, "current_rollout": currentRollout,
		"observed_inventory": counts,
	})
}

type DeliveryFleet struct {
	Summary       DeliveryFleetSummary       `json:"summary"`
	Clusters      []DeliveryFleetCluster     `json:"clusters"`
	Attention     []DeliveryFleetAttention   `json:"attention"`
	Distributions DeliveryFleetDistributions `json:"distributions"`
}

type DeliveryFleetSummary struct {
	AdoptedClusters int64 `json:"adopted_clusters"`
	FluxReady       int64 `json:"flux_ready"`
	Incompatible    int64 `json:"incompatible"`
	Disconnected    int64 `json:"disconnected"`
	Stale           int64 `json:"stale"`
	Assignments     int64 `json:"assignments"`
	Drifted         int64 `json:"drifted"`
	Failed          int64 `json:"failed"`
	Degraded        int64 `json:"degraded"`
	ActiveRollouts  int64 `json:"active_rollouts"`
}

type DeliveryFleetCluster struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	DisplayName         string     `json:"display_name"`
	IsLocal             bool       `json:"is_local"`
	Connected           bool       `json:"connected"`
	Stale               bool       `json:"stale"`
	PrivilegeProfile    string     `json:"privilege_profile"`
	KubernetesVersion   string     `json:"kubernetes_version"`
	AgentVersion        string     `json:"agent_version"`
	FluxVersion         string     `json:"flux_version"`
	CompatibilityStatus string     `json:"compatibility_status"`
	InventoryReady      bool       `json:"inventory_ready"`
	InventoryErrorCode  string     `json:"inventory_error_code"`
	AssignmentCount     int64      `json:"assignment_count"`
	ReadyCount          int64      `json:"ready_count"`
	FailedCount         int64      `json:"failed_count"`
	DegradedCount       int64      `json:"degraded_count"`
	DriftedCount        int64      `json:"drifted_count"`
	LastHeartbeat       *time.Time `json:"last_heartbeat"`
	InventoryObservedAt *time.Time `json:"inventory_observed_at"`
	LastObservedAt      *time.Time `json:"last_observed_at"`
}

type DeliveryFleetAttention struct {
	ClusterID   uuid.UUID `json:"cluster_id"`
	ClusterName string    `json:"cluster_name"`
	Severity    string    `json:"severity"`
	Reason      string    `json:"reason"`
	Detail      string    `json:"detail"`
}

type DeliveryFleetCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type DeliveryFleetDistributions struct {
	Compatibility    []DeliveryFleetCount `json:"compatibility"`
	Privilege        []DeliveryFleetCount `json:"privilege"`
	AssignmentPhases []DeliveryFleetCount `json:"assignment_phases"`
}

func (h *InventoryHandler) Fleet(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery inventory persistence is unavailable")
		return
	}
	rows, err := h.queries.ListDeliveryFleetClusters(r.Context())
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	activeRollouts, err := h.queries.CountActiveDeliveryRollouts(r.Context())
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	now := time.Now()
	if h.now != nil {
		now = h.now()
	}
	respondData(w, http.StatusOK, buildDeliveryFleet(rows, activeRollouts, now))
}

func buildDeliveryFleet(rows []sqlc.ListDeliveryFleetClustersRow, activeRollouts int64, now time.Time) DeliveryFleet {
	clusters := make([]DeliveryFleetCluster, 0, len(rows))
	attention := make([]DeliveryFleetAttention, 0)
	compatibilityCounts := map[string]int64{}
	privilegeCounts := map[string]int64{}
	phaseCounts := map[string]int64{}
	var summary DeliveryFleetSummary
	summary.ActiveRollouts = activeRollouts

	for _, row := range rows {
		cluster := fleetClusterFromRow(row, now)
		clusters = append(clusters, cluster)
		if item, ok := fleetAttentionFor(cluster); ok {
			attention = append(attention, item)
		}
		if cluster.IsLocal {
			continue
		}
		summary.AdoptedClusters++
		if !cluster.Connected {
			summary.Disconnected++
		}
		if cluster.Stale {
			summary.Stale++
		}
		if cluster.InventoryReady && cluster.CompatibilityStatus == "compatible" && cluster.Connected {
			summary.FluxReady++
		}
		if cluster.CompatibilityStatus == "incompatible" || cluster.CompatibilityStatus == "upgrade_required" || cluster.CompatibilityStatus == "degraded" {
			summary.Incompatible++
		}
		summary.Assignments += cluster.AssignmentCount
		summary.Drifted += cluster.DriftedCount
		summary.Failed += cluster.FailedCount
		summary.Degraded += cluster.DegradedCount
		compatibilityCounts[cluster.CompatibilityStatus]++
		privilegeCounts[cluster.PrivilegeProfile]++
		addFleetPhaseCounts(phaseCounts, row)
	}

	return DeliveryFleet{
		Summary:   summary,
		Clusters:  clusters,
		Attention: attention,
		Distributions: DeliveryFleetDistributions{
			Compatibility:    sortedFleetCounts(compatibilityCounts),
			Privilege:        sortedFleetCounts(privilegeCounts),
			AssignmentPhases: sortedFleetCounts(phaseCounts),
		},
	}
}

func fleetClusterFromRow(row sqlc.ListDeliveryFleetClustersRow, now time.Time) DeliveryFleetCluster {
	displayName := row.DisplayName
	if displayName == "" {
		displayName = row.Name
	}
	return DeliveryFleetCluster{
		ID:                  row.ID,
		Name:                row.Name,
		DisplayName:         displayName,
		IsLocal:             row.IsLocal,
		Connected:           row.Connected,
		Stale:               fleetRowIsStale(row, now),
		PrivilegeProfile:    fleetPrivilegeProfile(row.Annotations),
		KubernetesVersion:   row.KubernetesVersion,
		AgentVersion:        row.AgentVersion,
		FluxVersion:         row.FluxVersion,
		CompatibilityStatus: row.CompatibilityStatus,
		InventoryReady:      row.InventoryReady,
		InventoryErrorCode:  row.InventoryErrorCode,
		AssignmentCount:     row.AssignmentCount,
		ReadyCount:          row.ReadyCount,
		FailedCount:         row.FailedCount,
		DegradedCount:       row.DegradedCount,
		DriftedCount:        row.DriftedCount,
		LastHeartbeat:       optionalTimestamp(row.LastHeartbeat),
		InventoryObservedAt: optionalTimestamp(row.InventoryObservedAt),
		LastObservedAt:      optionalAnyTime(row.LastObservedAt),
	}
}

func fleetRowIsStale(row sqlc.ListDeliveryFleetClustersRow, now time.Time) bool {
	if row.IsLocal || !row.Connected {
		return false
	}
	if !row.LastHeartbeat.Valid || now.Sub(row.LastHeartbeat.Time) > fleetStaleAfter {
		return true
	}
	if row.InventoryObservedAt.Valid && now.Sub(row.InventoryObservedAt.Time) > fleetStaleAfter {
		return true
	}
	return false
}

func fleetAttentionFor(cluster DeliveryFleetCluster) (DeliveryFleetAttention, bool) {
	if cluster.IsLocal {
		return DeliveryFleetAttention{}, false
	}
	name := cluster.DisplayName
	item := DeliveryFleetAttention{ClusterID: cluster.ID, ClusterName: name}
	switch {
	case !cluster.Connected:
		item.Severity, item.Reason, item.Detail = "error", "disconnected", "Agent is not connected"
	case cluster.FailedCount > 0:
		item.Severity, item.Reason, item.Detail = "error", "failed", "One or more assignments failed"
	case cluster.CompatibilityStatus == "incompatible" || cluster.CompatibilityStatus == "upgrade_required":
		item.Severity, item.Reason, item.Detail = "error", cluster.CompatibilityStatus, cluster.InventoryErrorCode
		if item.Detail == "" {
			item.Detail = "Flux controllers are not compatible with the pinned distribution"
		}
	case cluster.CompatibilityStatus == "degraded" || cluster.DegradedCount > 0:
		item.Severity, item.Reason, item.Detail = "warning", "degraded", "Cluster or assignments are degraded"
	case cluster.DriftedCount > 0:
		item.Severity, item.Reason, item.Detail = "warning", "drifted", "Live objects drifted from desired spec"
	case cluster.Stale:
		item.Severity, item.Reason, item.Detail = "warning", "stale", "Heartbeat or controller inventory is older than 5 minutes"
	case cluster.CompatibilityStatus == "unknown":
		item.Severity, item.Reason, item.Detail = "warning", "inventory_missing", "No Flux controller inventory has been reported"
	default:
		return DeliveryFleetAttention{}, false
	}
	return item, true
}

func addFleetPhaseCounts(counts map[string]int64, row sqlc.ListDeliveryFleetClustersRow) {
	add := func(phase string, count int64) {
		if count > 0 {
			counts[phase] += count
		}
	}
	add("ready", row.ReadyCount)
	add("failed", row.FailedCount)
	add("degraded", row.DegradedCount)
	add("applying", row.ApplyingCount)
	add("unknown", row.UnknownCount)
	add("suspended", row.SuspendedCount)
	add("pending", row.PendingCount)
	add("blocked", row.BlockedCount)
	add("deleting", row.DeletingCount)
}

func sortedFleetCounts(values map[string]int64) []DeliveryFleetCount {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]DeliveryFleetCount, 0, len(keys))
	for _, key := range keys {
		out = append(out, DeliveryFleetCount{Key: key, Count: values[key]})
	}
	return out
}

func fleetPrivilegeProfile(raw json.RawMessage) string {
	if len(raw) == 0 {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	return agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation])
}

func optionalTimestamp(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func optionalAnyTime(value any) *time.Time {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return nil
		}
		return &typed
	case pgtype.Timestamptz:
		return optionalTimestamp(typed)
	default:
		return nil
	}
}
