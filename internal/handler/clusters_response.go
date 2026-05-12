package handler

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ClusterResponse is the explicit wire shape for /api/v1/clusters/ responses.
// It enumerates every field the legacy embed-sqlc.Cluster + clusterWithMetrics
// shape produced so schema-level changes (column add/rename/nullable shift,
// pgtype refactor) no longer silently change the dashboard API contract.
//
// JSON keys and value semantics are pinned to what the previous
// json.Marshal(clusterWithMetrics{Cluster: c, ...}) produced — see
// TestClusterResponse_WireCompat for the byte-for-byte assertion.
//
// pgtype unpack rules (matching pgx v5 native MarshalJSON):
//   - pgtype.Timestamptz → *string (RFC3339Nano when valid, nil when invalid)
//   - pgtype.UUID        → *string (uuid.String when valid, nil when invalid)
//
// The three metric scalars (cpu_percentage, memory_percentage, pod_count) are
// optional enrichment populated by ClusterHandler.enrichClusterFromCache /
// enrichClusterFresh; they default to zero when no metrics provider is wired.
type ClusterResponse struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	Status            string          `json:"status"`
	ApiServerUrl      string          `json:"api_server_url"`
	CaCertificate     string          `json:"ca_certificate"`
	Environment       string          `json:"environment"`
	Region            string          `json:"region"`
	Provider          string          `json:"provider"`
	Labels            json.RawMessage `json:"labels"`
	Annotations       json.RawMessage `json:"annotations"`
	Distribution      string          `json:"distribution"`
	AgentVersion      string          `json:"agent_version"`
	LastHeartbeat     *string         `json:"last_heartbeat"`
	KubernetesVersion string          `json:"kubernetes_version"`
	NodeCount         int32           `json:"node_count"`
	CreatedByID       *string         `json:"created_by_id"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
	IsLocal           bool            `json:"is_local"`
	DecommissionedAt  *string         `json:"decommissioned_at"`

	// Metric enrichment (added by clusterWithMetrics historically).
	CPUPercentage    float64 `json:"cpu_percentage"`
	MemoryPercentage float64 `json:"memory_percentage"`
	PodCount         int     `json:"pod_count"`
}

// clusterToResponse maps a sqlc.Cluster row into the explicit wire DTO.
// Metric fields default to zero; callers that have a metrics provider should
// overwrite them after the call.
func clusterToResponse(c sqlc.Cluster) ClusterResponse {
	resp := ClusterResponse{
		ID:                c.ID.String(),
		Name:              c.Name,
		DisplayName:       c.DisplayName,
		Description:       c.Description,
		Status:            c.Status,
		ApiServerUrl:      c.ApiServerUrl,
		CaCertificate:     c.CaCertificate,
		Environment:       c.Environment,
		Region:            c.Region,
		Provider:          c.Provider,
		Labels:            c.Labels,
		Annotations:       c.Annotations,
		Distribution:      c.Distribution,
		AgentVersion:      c.AgentVersion,
		KubernetesVersion: c.KubernetesVersion,
		NodeCount:         c.NodeCount,
		CreatedAt:         c.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:         c.UpdatedAt.Format(time.RFC3339Nano),
		IsLocal:           c.IsLocal,
	}
	if c.LastHeartbeat.Valid {
		s := c.LastHeartbeat.Time.Format(time.RFC3339Nano)
		resp.LastHeartbeat = &s
	}
	if c.DecommissionedAt.Valid {
		s := c.DecommissionedAt.Time.Format(time.RFC3339Nano)
		resp.DecommissionedAt = &s
	}
	if c.CreatedByID.Valid {
		s := uuid.UUID(c.CreatedByID.Bytes).String()
		resp.CreatedByID = &s
	}
	return resp
}

// Note: sqlc.ClusterDecommission is already wrapped by renderDecommission
// (clusters.go) into DecommissionStatusResponse — a richer DTO with parsed
// phases + a status URL. No raw sqlc row leaves the cluster decommission
// endpoints, so no additional DTO is needed here.
