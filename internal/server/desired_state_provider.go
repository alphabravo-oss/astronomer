package server

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// agentManifestRenderer is the narrow seam the desired-state provider needs from
// the cluster handler: render the agent's own install manifest for a cluster.
// Satisfied by *handler.ClusterHandler.RenderAgentManifestForCluster.
type agentManifestRenderer interface {
	RenderAgentManifestForCluster(ctx context.Context, clusterID uuid.UUID) (string, error)
}

// DesiredStateAdapter implements tunnel.DesiredStateProvider. It renders the
// Fleet-style PULL desired state for a cluster by combining the agent's own
// manifest (from the cluster handler) with the enabled baseline components
// (server.DesiredState reads enablement from platform_settings).
type DesiredStateAdapter struct {
	renderer agentManifestRenderer
	settings platformSettingReader
}

// NewDesiredStateAdapter wires the desired-state provider. The renderer is the
// *handler.ClusterHandler; settings is the *sqlc.Queries (platform settings
// reader) used to resolve which baseline components are enabled.
func NewDesiredStateAdapter(h *handler.ClusterHandler, settings platformSettingReader) *DesiredStateAdapter {
	return &DesiredStateAdapter{renderer: h, settings: settings}
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
	agentManifest, err := a.renderer.RenderAgentManifestForCluster(ctx, id)
	if err != nil {
		return protocol.DesiredStateResponsePayload{}, err
	}
	return DesiredState(ctx, clusterID, agentManifest, a.settings)
}
