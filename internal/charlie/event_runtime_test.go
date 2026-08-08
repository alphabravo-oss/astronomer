package charlie

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/google/uuid"
)

type eventRuntimeFake struct {
	triggerIngestFake
	connection sqlc.CharlieConnection
	rules      []sqlc.CharlieTriggerRule
	history    []sqlc.AgentConnectionEvent
	recorded   []sqlc.RecordAgentConnectionEventParams
	alert      sqlc.AlertEvent
	alertRule  sqlc.AlertRule
}

func (f *eventRuntimeFake) GetAlertEventByID(context.Context, uuid.UUID) (sqlc.AlertEvent, error) {
	return f.alert, nil
}
func (f *eventRuntimeFake) GetAlertRuleByID(context.Context, uuid.UUID) (sqlc.AlertRule, error) {
	return f.alertRule, nil
}

func (f *eventRuntimeFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *eventRuntimeFake) ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error) {
	return f.rules, nil
}
func (f *eventRuntimeFake) RecordAgentConnectionEvent(_ context.Context, p sqlc.RecordAgentConnectionEventParams) (sqlc.AgentConnectionEvent, error) {
	f.recorded = append(f.recorded, p)
	row := sqlc.AgentConnectionEvent{ID: uuid.New(), ClusterID: p.ClusterID, EventType: p.EventType, AgentVersion: p.AgentVersion, OccurredAt: p.OccurredAt}
	f.history = append([]sqlc.AgentConnectionEvent{row}, f.history...)
	return row, nil
}
func (f *eventRuntimeFake) CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error) {
	return f.history, nil
}

func TestEventRuntimePersistsConnectionMetadataAndEvaluatesEnabledRules(t *testing.T) {
	now := time.Unix(50000, 0).UTC()
	connection := readySessionConnection()
	disconnect := triggerRule("agent_disconnected", DefaultDisconnectGrace)
	flapping := triggerRule("agent_flapping", DefaultFlapWindow)
	store := &eventRuntimeFake{connection: connection, rules: []sqlc.CharlieTriggerRule{disconnect, flapping}, history: []sqlc.AgentConnectionEvent{
		{EventType: "disconnected", OccurredAt: now.Add(-10 * time.Minute)},
		{EventType: "disconnected", OccurredAt: now.Add(-5 * time.Minute)},
	}}
	runtime, _ := NewEventRuntime(events.NewBus(), store, func() bool { return true })
	runtime.now = func() time.Time { return now }
	runtime.ingestor.now = runtime.now
	clusterID := uuid.New()
	err := runtime.consume(context.Background(), events.Event{Type: events.TypeClusterDisconnected, Time: now, Data: map[string]any{"cluster_id": clusterID.String(), "session_id": "session-1", "agent_version": "1.2.3"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 || store.recorded[0].EventType != "disconnected" || store.recorded[0].ClusterID != clusterID {
		t.Fatalf("agent event was not persisted safely: %#v", store.recorded)
	}
	if len(store.created) != 2 {
		t.Fatalf("disconnect and flap rules were not evaluated: %#v", store.created)
	}
	for _, created := range store.created {
		if created.ResourceID != clusterID.String() || created.OriginEventRef == "" || created.NextAttemptAt.IsZero() {
			t.Fatalf("trigger lost durable product correlation: %#v", created)
		}
	}
}

func TestEventRuntimeReconnectSuppressesPendingDisconnectWithoutDispatch(t *testing.T) {
	now := time.Unix(60000, 0).UTC()
	connection := readySessionConnection()
	rule := triggerRule("agent_disconnected", DefaultDisconnectGrace)
	store := &eventRuntimeFake{connection: connection, rules: []sqlc.CharlieTriggerRule{rule}}
	runtime, _ := NewEventRuntime(events.NewBus(), store, func() bool { return true })
	clusterID := uuid.New()
	if err := runtime.consume(context.Background(), events.Event{Type: events.TypeClusterConnected, Time: now, Data: map[string]any{"cluster_id": clusterID.String(), "agent_version": "1.2.3"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.suppressed) != 1 || len(store.created) != 0 || store.recorded[0].EventType != "connected" {
		t.Fatalf("reconnect did not cancel pending disconnect safely: store=%#v", store)
	}
}

func TestEventRuntimeInactiveIgnoresEventsBeforePersistence(t *testing.T) {
	store := &eventRuntimeFake{connection: readySessionConnection()}
	runtime, _ := NewEventRuntime(events.NewBus(), store, func() bool { return false })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.Run(ctx)
	if len(store.recorded) != 0 || len(store.created) != 0 {
		t.Fatal("inactive event runtime wrote state")
	}
}

func TestEventRuntimeInactiveConstructsNoConsumerOrTimer(t *testing.T) {
	tests := []struct {
		name     string
		features gateFeature
		mutate   func(*eventRuntimeFake)
	}{
		{name: "feature false", features: gateFeature(false), mutate: func(*eventRuntimeFake) {}},
		{name: "connection inactive", features: gateFeature(true), mutate: func(store *eventRuntimeFake) { store.connection.Active = false }},
		{name: "operational disabled", features: gateFeature(true), mutate: func(store *eventRuntimeFake) {
			store.connection.RequestedMode, store.connection.VerifiedMode = string(ModeDisabled), string(ModeDisabled)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &eventRuntimeFake{connection: readySessionConnection()}
			test.mutate(store)
			runtime, _ := NewEventRuntime(events.NewBus(), store, func() bool {
				return EvaluateActivation(context.Background(), test.features, store).Runnable
			})
			timers, consumers := 0, 0
			runtime.ticker = func(time.Duration) runtimeTicker {
				timers++
				return &fakeRuntimeTicker{channel: make(chan time.Time)}
			}
			runtime.subscribe = func(context.Context) <-chan events.Event {
				consumers++
				return make(chan events.Event)
			}
			runtime.Run(context.Background())
			if timers != 0 || consumers != 0 || len(store.recorded) != 0 || len(store.created) != 0 {
				t.Fatalf("timers=%d consumers=%d recorded=%d created=%d", timers, consumers, len(store.recorded), len(store.created))
			}
		})
	}
}

func TestEventRuntimeTriggersOnlyUnscopedHighManagementAlerts(t *testing.T) {
	now := time.Unix(70000, 0).UTC()
	ruleID, alertID := uuid.New(), uuid.New()
	trigger := triggerRule("alert_high_critical", time.Minute)
	store := &eventRuntimeFake{
		connection: readySessionConnection(), rules: []sqlc.CharlieTriggerRule{trigger},
		alert:     sqlc.AlertEvent{ID: alertID, RuleID: ruleID, Status: "firing", FiredAt: now},
		alertRule: sqlc.AlertRule{ID: ruleID, Severity: "critical"},
	}
	runtime, _ := NewEventRuntime(events.NewBus(), store, func() bool { return true })
	if err := runtime.consume(context.Background(), events.Event{Type: events.TypeAlertingChanged, Time: now, Data: map[string]any{"id": alertID.String(), "kind": "event"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.created) != 1 || store.created[0].ResourceType != "alert" || store.created[0].ResourceID != alertID.String() {
		t.Fatalf("management alert trigger = %#v", store.created)
	}

	store.created = nil
	store.alert.ClusterID.Valid = true
	store.alert.ClusterID.Bytes = uuid.New()
	if err := runtime.consume(context.Background(), events.Event{Type: events.TypeAlertingChanged, Time: now, Data: map[string]any{"id": alertID.String(), "cluster_id": uuid.UUID(store.alert.ClusterID.Bytes).String(), "kind": "event"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.created) != 0 {
		t.Fatal("downstream-cluster alert crossed Charlie v1 management-plane boundary")
	}
}
