package crd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// captureIngester records every call so the watcher tests can assert
// dispatch happened (or didn't) and inspect the payload that was
// routed.
type captureIngester struct {
	calls []struct {
		ClusterID uuid.UUID
		Raw       any
	}
	err error
}

func (c *captureIngester) Ingest(_ context.Context, clusterID uuid.UUID, raw any) error {
	c.calls = append(c.calls, struct {
		ClusterID uuid.UUID
		Raw       any
	}{ClusterID: clusterID, Raw: raw})
	return c.err
}

func TestRouteTrivyEvent_MatchesAndRoutes(t *testing.T) {
	ci := &captureIngester{}
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "rep-1"},
	})
	ev := TrivyMirrorEvent{
		ClusterID:  uuid.New(),
		APIVersion: TrivyGroupVersion.String(),
		Kind:       TrivyVulnerabilityReportKind,
		Type:       "ADDED",
		Object:     body,
	}
	matched, err := RouteTrivyEvent(context.Background(), ev, ci)
	if !matched {
		t.Fatalf("expected matched=true")
	}
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(ci.calls) != 1 {
		t.Fatalf("expected one ingest call, got %d", len(ci.calls))
	}
	if ci.calls[0].ClusterID != ev.ClusterID {
		t.Fatalf("cluster_id mismatch")
	}
}

func TestRouteTrivyEvent_IgnoresOtherKinds(t *testing.T) {
	ci := &captureIngester{}
	ev := TrivyMirrorEvent{
		ClusterID:  uuid.New(),
		APIVersion: GroupVersion.String(), // management group, not trivy
		Kind:       "Cluster",
		Type:       "ADDED",
		Object:     json.RawMessage(`{}`),
	}
	matched, err := RouteTrivyEvent(context.Background(), ev, ci)
	if matched {
		t.Fatalf("expected matched=false on non-trivy kind")
	}
	if err != nil {
		t.Fatalf("non-matching events MUST be nil-error, got %v", err)
	}
	if len(ci.calls) != 0 {
		t.Fatalf("expected no ingest, got %d", len(ci.calls))
	}
}

func TestRouteTrivyEvent_DeletedIsNoOp(t *testing.T) {
	ci := &captureIngester{}
	ev := TrivyMirrorEvent{
		ClusterID:  uuid.New(),
		APIVersion: TrivyGroupVersion.String(),
		Kind:       TrivyVulnerabilityReportKind,
		Type:       "DELETED",
		Object:     json.RawMessage(`{"metadata":{"name":"rep-1"}}`),
	}
	matched, err := RouteTrivyEvent(context.Background(), ev, ci)
	if !matched {
		t.Fatalf("expected matched=true for delete event")
	}
	if err != nil {
		t.Fatalf("delete should be a no-op, got %v", err)
	}
	if len(ci.calls) != 0 {
		t.Fatalf("delete should not call ingester, got %d calls", len(ci.calls))
	}
}

func TestRouteTrivyEvent_NilClusterIDIsError(t *testing.T) {
	ci := &captureIngester{}
	ev := TrivyMirrorEvent{
		APIVersion: TrivyGroupVersion.String(),
		Kind:       TrivyVulnerabilityReportKind,
		Type:       "ADDED",
		Object:     json.RawMessage(`{}`),
	}
	matched, err := RouteTrivyEvent(context.Background(), ev, ci)
	if !matched {
		t.Fatalf("expected matched=true (kind matched, validation failed)")
	}
	if err == nil {
		t.Fatalf("expected error on nil cluster_id")
	}
}

func TestRouteTrivyEvent_PropagatesIngestError(t *testing.T) {
	ci := &captureIngester{err: errors.New("boom")}
	ev := TrivyMirrorEvent{
		ClusterID:  uuid.New(),
		APIVersion: TrivyGroupVersion.String(),
		Kind:       TrivyVulnerabilityReportKind,
		Type:       "ADDED",
		Object:     json.RawMessage(`{}`),
	}
	matched, err := RouteTrivyEvent(context.Background(), ev, ci)
	if !matched || err == nil {
		t.Fatalf("expected matched=true + err, got matched=%v err=%v", matched, err)
	}
}

func TestNewVulnIngesterAdapter_WrapsCallback(t *testing.T) {
	var got string
	adapter := NewVulnIngesterAdapter(func(_ context.Context, id uuid.UUID, raw any) error {
		got = id.String()
		_ = raw
		return nil
	})
	if adapter == nil {
		t.Fatalf("expected non-nil adapter")
	}
	cid := uuid.New()
	if err := adapter.Ingest(context.Background(), cid, "x"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got != cid.String() {
		t.Fatalf("callback not invoked with cluster_id")
	}
}
