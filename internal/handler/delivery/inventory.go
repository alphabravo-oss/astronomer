package delivery

import (
	"context"
	"errors"
	"net/http"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/compatibility"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type InventoryQueries interface {
	GetDeliveryControllerInventory(context.Context, sqlc.GetDeliveryControllerInventoryParams) (sqlc.DeliveryControllerInventory, error)
	ListClusterDeployments(context.Context, sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error)
	CountClusterDeployments(context.Context, sqlc.CountClusterDeploymentsParams) (int64, error)
	CountDeliveryControllerCompatibility(context.Context) ([]sqlc.CountDeliveryControllerCompatibilityRow, error)
	GetCurrentDeliverySystemRollout(context.Context) (sqlc.DeliverySystemRollout, error)
	ListDeliverySystemReleases(context.Context, sqlc.ListDeliverySystemReleasesParams) ([]sqlc.ListDeliverySystemReleasesRow, error)
}

type InventoryHandler struct {
	queries InventoryQueries
}

func NewInventoryHandler(queries InventoryQueries) *InventoryHandler {
	return &InventoryHandler{queries: queries}
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
