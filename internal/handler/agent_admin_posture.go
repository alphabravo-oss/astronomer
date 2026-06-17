// Package handler — cluster-admin agent posture report (Workstream E3).
//
// GATE-0 (B1) inverted the agent privilege-profile default so a cluster
// registered with no explicit profile resolves to read-only `viewer`
// instead of cluster-admin. Already-registered clusters whose stored
// annotation is `admin` are unaffected by that code change — they keep
// running a cluster-admin agent until an operator re-profiles them.
//
// This endpoint is the rollout aid for that migration: it enumerates
// every managed cluster whose agent still resolves to the `admin`
// (cluster-admin) privilege profile, so operators can find and re-profile
// them deliberately.
//
//	GET /api/v1/admin/agents/cluster-admin-posture/
//
// Superuser-gated inside the handler (same pattern as admin_drill.go) so
// the failure mode is a clean 401 (anon) / 403 (non-superuser). Read-only.
package handler

import (
	"net/http"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// clusterAdminPostureItem is the wire shape for a single cluster whose
// agent resolves to the cluster-admin privilege profile.
type clusterAdminPostureItem struct {
	ClusterID        string `json:"cluster_id"`
	ClusterName      string `json:"cluster_name"`
	ClusterStatus    string `json:"cluster_status"`
	IsLocal          bool   `json:"is_local"`
	PrivilegeProfile string `json:"privilege_profile"`
}

// clusterAdminPostureResponse is the body of the posture report. Items
// holds ONLY the clusters resolving to the admin profile; TotalClusters
// is the size of the scanned fleet so the dashboard can render
// "N of M clusters still run cluster-admin agents".
type clusterAdminPostureResponse struct {
	AdminProfileClusters int                       `json:"admin_profile_clusters"`
	TotalClusters        int                       `json:"total_clusters"`
	Items                []clusterAdminPostureItem `json:"items"`
}

// ClusterAdminPosture handles GET /api/v1/admin/agents/cluster-admin-posture/.
// It returns every cluster whose stored privilege-profile annotation
// resolves (via the canonical agenttemplate.NormalizePrivilegeProfile) to
// the cluster-admin `admin` profile.
func (h *AgentFleetHandler) ClusterAdminPosture(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Admin store not configured",
		ForbiddenMessage:        "Cluster-admin posture report requires superuser privileges",
	}); !ok {
		return
	}

	// Page through the whole fleet so no cluster is missed regardless of
	// fleet size — the report's value is being exhaustive.
	const pageSize int32 = 500
	items := make([]clusterAdminPostureItem, 0)
	total := 0
	for offset := int32(0); ; offset += pageSize {
		clusters, err := h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		total += len(clusters)
		for _, cluster := range clusters {
			profile := agentPrivilegeProfileFromAnnotations(cluster.Annotations)
			if profile != agenttemplate.PrivilegeProfileAdmin {
				continue
			}
			items = append(items, clusterAdminPostureItem{
				ClusterID:        cluster.ID.String(),
				ClusterName:      cluster.Name,
				ClusterStatus:    cluster.Status,
				IsLocal:          cluster.IsLocal,
				PrivilegeProfile: profile,
			})
		}
		if int32(len(clusters)) < pageSize {
			break
		}
	}

	recordAudit(r, h.queries, "admin.agent_cluster_admin_posture.viewed", "platform", "", "agent_posture", map[string]any{
		"admin_profile_clusters": len(items),
		"total_clusters":         total,
	})

	RespondJSON(w, http.StatusOK, clusterAdminPostureResponse{
		AdminProfileClusters: len(items),
		TotalClusters:        total,
		Items:                items,
	})
}
