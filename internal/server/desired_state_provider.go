package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// agentManifestRenderer is the narrow seam the desired-state provider needs from
// the cluster handler: render the agent's own install manifest for a cluster.
// Satisfied by *handler.ClusterHandler.RenderAgentManifestForCluster.
type agentManifestRenderer interface {
	RenderAgentManifestForCluster(ctx context.Context, clusterID uuid.UUID) (string, error)
}

// decommissionStateReader is the narrow seam used to TOMBSTONE the desired
// state mid-teardown (M14): if a pending/running decommission row exists for a
// cluster, the provider withholds the desired state so a pull agent that
// restarts mid-decommission cannot re-apply the baseline it's being torn down
// from. Satisfied by *sqlc.Queries.
type decommissionStateReader interface {
	GetLatestClusterDecommissionByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterDecommission, error)
}

// DesiredStateAdapter implements tunnel.DesiredStateProvider. It renders the
// Fleet-style PULL desired state for a cluster by combining the agent's own
// manifest (from the cluster handler) with the enabled baseline components
// (server.DesiredState reads enablement from platform_settings).
type DesiredStateAdapter struct {
	renderer agentManifestRenderer
	settings platformSettingReader
	decomm   decommissionStateReader
}

// NewDesiredStateAdapter wires the desired-state provider. The renderer is the
// *handler.ClusterHandler; settings is the *sqlc.Queries (platform settings
// reader) used to resolve which baseline components are enabled; decomm is the
// *sqlc.Queries used to withhold desired state for a decommissioning cluster.
func NewDesiredStateAdapter(h *handler.ClusterHandler, settings platformSettingReader, decomm decommissionStateReader) *DesiredStateAdapter {
	return &DesiredStateAdapter{renderer: h, settings: settings, decomm: decomm}
}

// DesiredState renders the desired-state response for a cluster. It satisfies
// tunnel.DesiredStateProvider. currentRevision is accepted for future
// short-circuiting; the MVP always re-renders (the render is cheap and the
// revision lets the agent skip an unchanged apply).
func (a *DesiredStateAdapter) DesiredState(ctx context.Context, clusterID string, currentRevision string) (protocol.DesiredStateResponsePayload, error) {
	if a == nil || a.renderer == nil {
		return protocol.DesiredStateResponsePayload{}, fmt.Errorf("desired state adapter not configured")
	}
	id, err := uuid.Parse(clusterID)
	if err != nil {
		return protocol.DesiredStateResponsePayload{}, fmt.Errorf("invalid cluster id %q: %w", clusterID, err)
	}
	// Tombstone gate (M14): withhold the desired state while a decommission is
	// in flight so a pull agent restarting mid-teardown can't re-apply the
	// baseline. This is DB-derived (durable across an agent pod restart, unlike
	// the in-memory pause atomic) and returns an ERROR — the agent's
	// applyResponse logs-and-skips on a non-empty msg.Error, performing NO
	// apply and NO prune (universally back-compatible, no protocol change). A
	// healthy cluster has no pending/running row → renders normally; any read
	// error (incl. pgx.ErrNoRows for never-decommissioned clusters) falls
	// through to a normal render.
	if a.decomm != nil {
		if row, derr := a.decomm.GetLatestClusterDecommissionByCluster(ctx, id); derr == nil {
			if row.Status == "pending" || row.Status == "running" {
				return protocol.DesiredStateResponsePayload{}, errors.New("cluster decommissioning: desired state withheld")
			}
		}
	}
	agentManifest, err := a.renderer.RenderAgentManifestForCluster(ctx, id)
	if err != nil {
		return protocol.DesiredStateResponsePayload{}, err
	}
	return DesiredState(ctx, clusterID, agentManifest, a.settings)
}
