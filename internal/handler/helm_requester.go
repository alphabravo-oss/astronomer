package handler

import (
	"context"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmRequester is the API-layer interface for dispatching Helm operations
// through the agent tunnel.
type HelmRequester interface {
	Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error)
	Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error)
}

// TunnelHelmRequester implements HelmRequester by forwarding to the
// per-cluster Hub originator (Hub.SendHelmRequest), which handles correlation,
// timeouts, and stream cleanup.
type TunnelHelmRequester struct {
	hub *tunnel.Hub
}

func NewTunnelHelmRequester(hub *tunnel.Hub) *TunnelHelmRequester {
	return &TunnelHelmRequester{hub: hub}
}

func (r *TunnelHelmRequester) Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	if r == nil || r.hub == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	return r.hub.SendHelmRequest(ctx, clusterID, msgType, payload)
}

func (r *TunnelHelmRequester) Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error) {
	return r.Do(ctx, clusterID, protocol.MsgHelmStatus, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   namespace,
	})
}
