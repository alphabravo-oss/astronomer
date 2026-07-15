package tasks

// P4.9 — worker-side alerting.changed publishers: the alert-event
// ingestion/resolution path and the anomaly-baseline recompute publish on
// the runtime bus (Redis-attached in the dedicated worker process) so alert
// pages stay live while the SSE stream is open.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

func alertingPublishSubscribe(t *testing.T, bus *events.Bus) <-chan events.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return bus.Subscribe(ctx)
}

func alertingPublishReceive(t *testing.T, ch <-chan events.Event) map[string]any {
	t.Helper()
	select {
	case e := <-ch:
		if e.Type != events.TypeAlertingChanged {
			t.Fatalf("event type = %q, want %q", e.Type, events.TypeAlertingChanged)
		}
		raw, err := json.Marshal(e.Data)
		if err != nil {
			t.Fatalf("marshal event data: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal event data: %v", err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for alerting.changed")
		return nil
	}
}

func TestPublishAlertEventChanged_CarriesKindAndCluster(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	bus := events.NewBus()
	ch := alertingPublishSubscribe(t, bus)
	runtimeDeps = RuntimeDependencies{Bus: bus}

	clusterID := uuid.New()
	eventID := uuid.New()
	publishAlertEventChanged(pgtype.UUID{Bytes: clusterID, Valid: true}, eventID)

	payload := alertingPublishReceive(t, ch)
	if payload["cluster_id"] != clusterID.String() {
		t.Fatalf("cluster_id = %v, want %q (SEC-R07 drops events without it)", payload["cluster_id"], clusterID.String())
	}
	if payload["kind"] != "event" {
		t.Fatalf("kind = %v, want event", payload["kind"])
	}
	if payload["id"] != eventID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], eventID.String())
	}
}

func TestPublishAlertEventChanged_InvalidClusterIsUnscopedAndNilBusSafe(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	// Nil bus must be a no-op, never a panic.
	runtimeDeps = RuntimeDependencies{}
	publishAlertEventChanged(pgtype.UUID{}, uuid.New())

	bus := events.NewBus()
	ch := alertingPublishSubscribe(t, bus)
	runtimeDeps = RuntimeDependencies{Bus: bus}
	publishAlertEventChanged(pgtype.UUID{}, uuid.New())

	payload := alertingPublishReceive(t, ch)
	if _, ok := payload["cluster_id"]; ok {
		t.Fatalf("invalid cluster must publish unscoped, got cluster_id %v", payload["cluster_id"])
	}
}

// A recompute pass publishes ONE baseline event per distinct cluster —
// per-row events would spam the bus every 5 minutes.
func TestBaselineRecompute_PublishesOncePerCluster(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	for _, metric := range []string{"cpu_percent", "memory_percent"} {
		key := baselineKey(clusterID, metric, 3600)
		q.baselines[key] = sqlc.AnomalyBaseline{
			ID:            uuid.New(),
			ClusterID:     clusterID,
			MetricName:    metric,
			WindowSeconds: 3600,
			RecentSamples: json.RawMessage("[]"),
		}
	}

	bus := events.NewBus()
	ch := alertingPublishSubscribe(t, bus)
	runtimeDeps = RuntimeDependencies{Queries: nil, Bus: bus}

	if err := RunAnomalyBaselineRecompute(context.Background(), q, time.Now().UTC()); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	payload := alertingPublishReceive(t, ch)
	if payload["kind"] != "baseline" {
		t.Fatalf("kind = %v, want baseline", payload["kind"])
	}
	if payload["cluster_id"] != clusterID.String() {
		t.Fatalf("cluster_id = %v, want %q", payload["cluster_id"], clusterID.String())
	}
	select {
	case e := <-ch:
		if e.Type == events.TypeAlertingChanged {
			t.Fatalf("expected one event per distinct cluster, got extra %v", e.Data)
		}
	case <-time.After(100 * time.Millisecond):
	}
}
