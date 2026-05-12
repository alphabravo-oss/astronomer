// Sprint 062 — Trivy-operator VulnerabilityReport mirror.
//
// The CRD mirror in each managed cluster streams VulnerabilityReport
// CRs back to the management plane via the agent tunnel. The agent
// emits unstructured event payloads (object + cluster_id); the
// watcher below adapts those events into typed
// internal/scanner.TrivyVulnerabilityReport values and forwards them
// to the package's Ingester.
//
// This file deliberately uses ONLY the interface contract internal/
// scanner exposes — no import on internal/scanner itself — so the crd
// package keeps its existing zero-deps-on-handler-graph property.

package crd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// VulnIngester is the slice of internal/scanner.Ingester the watcher
// needs. We accept the interface so this package doesn't have to
// import internal/scanner directly — server-side glue wires the
// concrete *scanner.Ingester in.
type VulnIngester interface {
	Ingest(ctx context.Context, clusterID uuid.UUID, raw any) error
}

// vulnIngesterAdapter is the convenience adapter from a "raw any" callback
// to the typed scanner.Ingester.Ingest(ctx, clusterID, TrivyVulnerabilityReport)
// surface. We expose the typed shape externally; internally we go
// through `any` so this package doesn't import scanner.
type vulnIngesterAdapter struct {
	Forward func(ctx context.Context, clusterID uuid.UUID, raw any) error
}

func (v vulnIngesterAdapter) Ingest(ctx context.Context, clusterID uuid.UUID, raw any) error {
	if v.Forward == nil {
		return errors.New("crd: VulnIngester Forward callback is nil")
	}
	return v.Forward(ctx, clusterID, raw)
}

// NewVulnIngesterAdapter wraps a forward callback into a VulnIngester so
// callers in cmd/server can pass internal/scanner.Ingester.Ingest as a
// closure.
func NewVulnIngesterAdapter(forward func(ctx context.Context, clusterID uuid.UUID, raw any) error) VulnIngester {
	return vulnIngesterAdapter{Forward: forward}
}

// TrivyMirrorEvent is the on-wire shape the agent forwards over the
// CRD-mirror channel: a single envelope per CR with the cluster_id, the
// CR's kind/api-version, the change type (ADDED/MODIFIED/DELETED) and
// the raw object body. The watcher matches on (apiVersion, kind) +
// TrivyGroupVersion / TrivyVulnerabilityReportKind before routing into
// the ingester.
type TrivyMirrorEvent struct {
	ClusterID  uuid.UUID       `json:"cluster_id"`
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Type       string          `json:"type"`
	Object     json.RawMessage `json:"object"`
}

// RouteTrivyEvent dispatches one incoming mirror event into the
// ingester when (and only when) its (apiVersion, kind) matches the
// Trivy VulnerabilityReport CRD. Returns:
//
//   - (true, nil)  on a successful route
//   - (false, nil) for events that don't match this watcher's filter
//   - (false, err) on a routing failure (decode error, ingest error)
//
// Callers should keep listening even after a (false, err) — the next
// event for a different report may succeed.
func RouteTrivyEvent(ctx context.Context, ev TrivyMirrorEvent, ingester VulnIngester) (matched bool, err error) {
	if ingester == nil {
		return false, errors.New("crd: ingester is required")
	}
	if ev.APIVersion != TrivyGroupVersion.String() || ev.Kind != TrivyVulnerabilityReportKind {
		return false, nil
	}
	if ev.ClusterID == uuid.Nil {
		return true, errors.New("crd: trivy event missing cluster_id")
	}
	// DELETE: we drop the report row by relying on the upstream
	// chain ON DELETE CASCADE; the controller mirrors the deletion by
	// not re-ingesting. The brief asks for re-ingest replacement of
	// CVE rows on UPDATE, which the Ingester already handles. Today
	// we route ADDED and MODIFIED events through Ingest; DELETED
	// events are surfaced as a matched=true,err=nil no-op so the
	// caller's filter loop can drop them too. Operators can wipe a
	// stale row from the DB by cluster_id + report_name if needed.
	if ev.Type == "DELETED" {
		return true, nil
	}
	// We don't decode into the strongly-typed scanner shape here —
	// that would force a circular import. Instead we hand the raw
	// JSON body to the ingester adapter which converts inside its
	// own package boundary.
	var raw map[string]any
	if err := json.Unmarshal(ev.Object, &raw); err != nil {
		return true, fmt.Errorf("crd: decode trivy event object: %w", err)
	}
	if err := ingester.Ingest(ctx, ev.ClusterID, raw); err != nil {
		return true, fmt.Errorf("crd: ingest trivy event: %w", err)
	}
	return true, nil
}
