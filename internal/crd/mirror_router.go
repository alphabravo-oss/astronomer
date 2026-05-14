// Sprint 069 — CRD-mirror v2 server-side router.
//
// MirrorRouter is the small adapter the tunnel handler calls when an
// agent emits a protocol.MsgMirrorEvent frame (see
// internal/tunnel/handler.go::handleMirrorEvent). It satisfies the
// tunnel.MirrorIngester interface and dispatches into the per-kind
// Ingest* / Delete helpers in ingest_v2.go.
//
// The split between router and ingester keeps the wire-format decode
// (envelope → typed payload → unstructured) in one place and the
// DB-layer upsert in another — same pattern as sprint-014's
// ClusterSync / ProjectSync adapter shape.

package crd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// MirrorRouter wires a MirrorQuerier into the tunnel.MirrorIngester
// interface. Construct one at server boot, pass it to
// hub.SetMirrorIngester. Zero-value safe: a nil router is treated as
// "no ingester wired" by the hub.
type MirrorRouter struct {
	Queries MirrorQuerier
	// Vuln is the trivy-operator VulnerabilityReport ingester. Optional;
	// when nil, VulnerabilityReport events are matched + dropped (so
	// they don't fall into the unknown-kind error path that would
	// noisy-log every report on every reconnect).
	Vuln VulnIngester
}

// NewMirrorRouter constructs a router. q must not be nil; the hub-side
// nil-safety gate fires before we get here. vuln may be nil — see the
// field doc on MirrorRouter.Vuln.
func NewMirrorRouter(q MirrorQuerier) *MirrorRouter {
	return &MirrorRouter{Queries: q}
}

// SetVulnIngester wires the trivy VulnerabilityReport ingester. Returns
// the router so callers can chain it onto NewMirrorRouter in one
// expression. Pass nil to disable VulnerabilityReport mirroring (the
// kind will still match — events get dropped rather than erroring).
func (r *MirrorRouter) SetVulnIngester(v VulnIngester) *MirrorRouter {
	if r != nil {
		r.Vuln = v
	}
	return r
}

// RouteMirrorEvent decodes a payload and dispatches to the right
// per-kind helper. Implements the tunnel.MirrorIngester interface.
//
// Failure modes:
//   - Unknown kind → error, logged + dropped by the caller (no retry).
//   - Marshal/unmarshal failure → error, same disposition.
//   - DB error → propagated to caller; agent's next resync will retry.
func (r *MirrorRouter) RouteMirrorEvent(ctx context.Context, clusterID uuid.UUID, payload protocol.MirrorEventPayload) error {
	if r == nil || r.Queries == nil {
		return fmt.Errorf("mirror router: nil queries")
	}

	if payload.Op == protocol.MirrorOpDeleted {
		return HandleDelete(ctx, r.Queries, clusterID, DeleteEvent{
			Kind:      payload.Kind,
			Namespace: payload.Namespace,
			Name:      payload.Name,
		})
	}

	// Add / Modified — both go through the upsert path.
	if len(payload.Object) == 0 {
		return fmt.Errorf("mirror router: %s op=%s missing object body", payload.Kind, payload.Op)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload.Object, &raw); err != nil {
		return fmt.Errorf("mirror router: decode object: %w", err)
	}
	obj := &unstructured.Unstructured{Object: raw}

	switch payload.Kind {
	case KindIngressClass:
		_, err := IngestIngressClass(ctx, r.Queries, clusterID, obj)
		return err
	case KindGatewayClass:
		_, err := IngestGatewayClass(ctx, r.Queries, clusterID, obj)
		return err
	case KindNetworkPolicy:
		_, err := IngestNetworkPolicy(ctx, r.Queries, clusterID, obj)
		return err
	case KindResourceQuota:
		_, err := IngestResourceQuota(ctx, r.Queries, clusterID, obj)
		return err
	case KindLimitRange:
		_, err := IngestLimitRange(ctx, r.Queries, clusterID, obj)
		return err
	case KindVulnerabilityReport:
		if r.Vuln == nil {
			// Trivy ingester not wired — drop the event quietly. The
			// agent will keep emitting on every resync; logging every
			// drop would flood the server log on clusters with a busy
			// scanner. The "no reports in the dashboard" symptom is the
			// signal for operators to wire the ingester at boot.
			return nil
		}
		return r.Vuln.Ingest(ctx, clusterID, raw)
	default:
		return fmt.Errorf("mirror router: unknown kind %q", payload.Kind)
	}
}
