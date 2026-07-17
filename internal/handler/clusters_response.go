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
	ID                      string          `json:"id"`
	Name                    string          `json:"name"`
	DisplayName             string          `json:"display_name"`
	Description             string          `json:"description"`
	Status                  string          `json:"status"`
	ApiServerUrl            string          `json:"api_server_url"`
	CaCertificate           string          `json:"ca_certificate"`
	Environment             string          `json:"environment"`
	Region                  string          `json:"region"`
	Provider                string          `json:"provider"`
	Labels                  json.RawMessage `json:"labels"`
	Annotations             json.RawMessage `json:"annotations"`
	Distribution            string          `json:"distribution"`
	AgentVersion            string          `json:"agent_version"`
	LastHeartbeat           *string         `json:"last_heartbeat"`
	KubernetesVersion       string          `json:"kubernetes_version"`
	NodeCount               int32           `json:"node_count"`
	CreatedByID             *string         `json:"created_by_id"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
	IsLocal                 bool            `json:"is_local"`
	DecommissionedAt        *string         `json:"decommissioned_at"`
	ClusterUid              string          `json:"cluster_uid"`
	GroupID                 *string         `json:"group_id"`
	RegistrationPhase       string          `json:"registration_phase"`
	RegistrationStartedAt   *string         `json:"registration_started_at"`
	RegistrationCompletedAt *string         `json:"registration_completed_at"`
	InstallBaseline         *bool           `json:"install_baseline"`
	ManagedBy               string          `json:"managed_by"`
	ExternalRefApiVersion   string          `json:"external_ref_api_version"`
	ExternalRefKind         string          `json:"external_ref_kind"`
	ExternalRefNamespace    string          `json:"external_ref_namespace"`
	ExternalRefName         string          `json:"external_ref_name"`
	ObservedGeneration      int64           `json:"observed_generation"`

	// Metric enrichment (added by clusterWithMetrics historically).
	CPUPercentage    float64 `json:"cpu_percentage"`
	MemoryPercentage float64 `json:"memory_percentage"`
	PodCount         int     `json:"pod_count"`
	// MetricsServerPresent (C3 / M13) distinguishes the metrics-card states the
	// scalar zeros could not: false = the cluster has no metrics-server (CPU/mem
	// are zero because no metrics source exists), true = metrics-server is
	// present (zeros, if any, mean "no recent sample"). The richer
	// flowing/stale/no-server tri-state is also surfaced via the MetricsAvailable
	// cluster condition.
	MetricsServerPresent bool `json:"metrics_server_present"`

	// Decommissioning is true while an async decommission is in flight
	// (a cluster_decommissions row in 'pending'/'running') but the cluster
	// hasn't been tombstoned yet. The row stays in the list with this flag so
	// the dashboard can show a stable "Decommissioning" state instead of the
	// delete optimistically hiding the row and flickering until the worker
	// finishes. Once tombstoned (decommissioned_at set) the row leaves the list.
	Decommissioning bool `json:"decommissioning"`

	AgentPrivilegeProfile string               `json:"agent_privilege_profile"`
	ArgoCD                ClusterArgoCDSummary `json:"argocd"`
}

type ClusterArgoCDSummary struct {
	Registered         bool                            `json:"registered"`
	InstanceCount      int                             `json:"instance_count"`
	ClusterSecretNames []string                        `json:"cluster_secret_names"`
	BaselineManagedBy  string                          `json:"baseline_managed_by"`
	BaselineComponents []ClusterBaselineComponentOwner `json:"baseline_components"`
	Drift              ClusterArgoCDDriftSummary       `json:"drift"`
}

type ClusterArgoCDDriftSummary struct {
	AppCount             int     `json:"app_count"`
	SyncedCount          int     `json:"synced_count"`
	OutOfSyncCount       int     `json:"out_of_sync_count"`
	UnknownSyncCount     int     `json:"unknown_sync_count"`
	HealthyCount         int     `json:"healthy_count"`
	ProgressingCount     int     `json:"progressing_count"`
	DegradedCount        int     `json:"degraded_count"`
	UnknownHealthCount   int     `json:"unknown_health_count"`
	ResourceCreatedCount int     `json:"resource_created_count"`
	ResourceChangedCount int     `json:"resource_changed_count"`
	ResourcePrunedCount  int     `json:"resource_pruned_count"`
	LastSynced           *string `json:"last_synced,omitempty"`
	LastError            string  `json:"last_error,omitempty"`
}

type ClusterBaselineComponentOwner struct {
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	Namespace          string `json:"namespace"`
	ApplicationSetName string `json:"application_set_name"`
	ManagedBy          string `json:"managed_by"`
}

// clusterToResponse maps a sqlc.Cluster row into the explicit wire DTO.
// Metric fields default to zero; callers that have a metrics provider should
// overwrite them after the call.
func clusterToResponse(c sqlc.Cluster) ClusterResponse {
	resp := ClusterResponse{
		ID:   c.ID.String(),
		Name: c.Name,
		// display_name is optional and unset on most clusters — anything not
		// named through the wizard (a raw manifest attach, an agent
		// self-registration) only ever gets `name`. Callers overwhelmingly render
		// this straight into a heading, so returning "" left the cluster's name
		// blank on its own detail page. Fall back to name here, in the one
		// mapping every cluster response goes through, rather than asking each
		// call site to remember. Matches the ownership endpoint, which has always
		// coalesced these two.
		DisplayName: firstNonEmptyAgentValue(c.DisplayName, c.Name),
		Description:           c.Description,
		Status:                c.Status,
		ApiServerUrl:          c.ApiServerUrl,
		CaCertificate:         c.CaCertificate,
		Environment:           c.Environment,
		Region:                c.Region,
		Provider:              c.Provider,
		Labels:                c.Labels,
		Annotations:           c.Annotations,
		Distribution:          c.Distribution,
		AgentVersion:          c.AgentVersion,
		KubernetesVersion:     c.KubernetesVersion,
		NodeCount:             c.NodeCount,
		CreatedAt:             c.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:             c.UpdatedAt.Format(time.RFC3339Nano),
		IsLocal:               c.IsLocal,
		ClusterUid:            c.ClusterUid,
		RegistrationPhase:     c.RegistrationPhase,
		ManagedBy:             c.ManagedBy,
		ExternalRefApiVersion: c.ExternalRefApiVersion,
		ExternalRefKind:       c.ExternalRefKind,
		ExternalRefNamespace:  c.ExternalRefNamespace,
		ExternalRefName:       c.ExternalRefName,
		ObservedGeneration:    c.ObservedGeneration,
		AgentPrivilegeProfile: clusterAgentPrivilegeProfile(c.Annotations),
		ArgoCD: ClusterArgoCDSummary{
			BaselineManagedBy:  "unknown",
			BaselineComponents: baselineComponentOwnership("unknown"),
		},
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
	if c.GroupID.Valid {
		s := uuid.UUID(c.GroupID.Bytes).String()
		resp.GroupID = &s
	}
	if c.RegistrationStartedAt.Valid {
		s := c.RegistrationStartedAt.Time.Format(time.RFC3339Nano)
		resp.RegistrationStartedAt = &s
	}
	if c.RegistrationCompletedAt.Valid {
		s := c.RegistrationCompletedAt.Time.Format(time.RFC3339Nano)
		resp.RegistrationCompletedAt = &s
	}
	if c.InstallBaseline.Valid {
		b := c.InstallBaseline.Bool
		resp.InstallBaseline = &b
	}
	return resp
}

// Note: sqlc.ClusterDecommission is already wrapped by renderDecommission
// (clusters.go) into DecommissionStatusResponse — a richer DTO with parsed
// phases + a status URL. No raw sqlc row leaves the cluster decommission
// endpoints, so no additional DTO is needed here.
