package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type eventRuntimeQueries interface {
	triggerIngestQueries
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error)
	RecordAgentConnectionEvent(context.Context, sqlc.RecordAgentConnectionEventParams) (sqlc.AgentConnectionEvent, error)
	CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error)
}

// EventRuntime converts Astronomer-owned lifecycle events into durable,
// product-evaluated trigger intents. It ignores Redis-relayed copies, never
// opens a downstream tunnel, and performs no work while Charlie is inactive.
type EventRuntime struct {
	bus       *events.Bus
	queries   eventRuntimeQueries
	ingestor  *TriggerIngestor
	active    func() bool
	now       func() time.Time
	sweeper   *TriggerSweeper
	retention *RetentionService
}

func NewEventRuntime(bus *events.Bus, queries eventRuntimeQueries, active func() bool) (*EventRuntime, error) {
	if bus == nil || queries == nil || active == nil {
		return nil, fmt.Errorf("Charlie event runtime dependencies are incomplete")
	}
	ingestor, err := NewTriggerIngestor(queries, active)
	if err != nil {
		return nil, err
	}
	runtime := &EventRuntime{bus: bus, queries: queries, ingestor: ingestor, active: active, now: time.Now}
	if sweepQueries, ok := queries.(triggerSweepQueries); ok {
		runtime.sweeper = NewTriggerSweeper(sweepQueries, ingestor, active)
	}
	if retained, ok := queries.(retentionQueries); ok {
		runtime.retention = NewRetentionService(retained, active)
	}
	return runtime, nil
}

func (r *EventRuntime) Run(ctx context.Context) {
	if r == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if !r.active() {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		activeCtx, cancel := context.WithCancel(ctx)
		stream := r.bus.Subscribe(activeCtx)
		run := true
		nextSweep := time.Time{}
		nextRetention := time.Time{}
		for run {
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-ticker.C:
				if !r.active() {
					run = false
				} else if r.sweeper != nil && !r.now().Before(nextSweep) {
					r.sweeper.now = r.now
					_ = r.sweeper.Sweep(ctx)
					nextSweep = r.now().Add(time.Minute)
				}
				if r.active() && r.retention != nil && !r.now().Before(nextRetention) {
					r.retention.now = r.now
					_ = r.retention.Run(ctx)
					nextRetention = r.now().Add(24 * time.Hour)
				}
			case event, ok := <-stream:
				if !ok {
					run = false
					continue
				}
				if event.Remote || !r.active() {
					continue
				}
				_ = r.consume(ctx, event)
			}
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *EventRuntime) consume(ctx context.Context, event events.Event) error {
	if event.Type == events.TypeAlertingChanged {
		return r.consumeManagementAlert(ctx, event)
	}
	switch event.Type {
	case events.TypeClusterConnected, events.TypeAgentReconnecting, events.TypeClusterDisconnected, events.TypeAgentFailed:
	default:
		return nil
	}
	payload, err := boundedAgentEventPayload(event.Data)
	if err != nil {
		return err
	}
	clusterID, err := uuid.Parse(payload.ClusterID)
	if err != nil {
		return fmt.Errorf("Charlie agent event cluster is invalid")
	}
	eventType, reason := productAgentEventType(event.Type)
	recorded, err := r.queries.RecordAgentConnectionEvent(ctx, sqlc.RecordAgentConnectionEventParams{
		ClusterID: clusterID, ConnectionID: pgtype.UUID{}, EventType: eventType,
		ReasonCode: reason, AgentVersion: payload.AgentVersion, Metadata: json.RawMessage(`{}`), OccurredAt: event.Time.UTC(),
	})
	if err != nil {
		return fmt.Errorf("record Astronomer agent connection event: %w", err)
	}
	connection, err := r.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return nil
	}
	rules, err := r.queries.ListEnabledCharlieTriggerRules(ctx, connection.ID)
	if err != nil {
		return fmt.Errorf("load Charlie trigger rules: %w", err)
	}

	if event.Type == events.TypeClusterConnected {
		for _, rule := range rules {
			if rule.Name != "agent_disconnected" {
				continue
			}
			original := agentTriggerSignal("agent_disconnected", clusterID, payload.AgentVersion, "disconnected", "transport_disconnected", event.Time)
			_, _ = r.ingestor.CancelDisconnected(ctx, rule, original)
		}
		return nil
	}

	maximumWindow := DefaultFlapWindow
	for _, rule := range rules {
		if duration := time.Duration(rule.WindowSeconds) * time.Second; duration > maximumWindow {
			maximumWindow = duration
		}
	}
	history, _ := r.queries.CharlieAgentConnectionHistory(ctx, sqlc.CharlieAgentConnectionHistoryParams{ClusterID: clusterID, Since: r.now().UTC().Add(-maximumWindow), RowLimit: 100})
	observations := make([]ConnectionObservation, 0, len(history))
	for _, item := range history {
		observations = append(observations, ConnectionObservation{State: item.EventType, OccurredAt: item.OccurredAt})
	}
	for _, rule := range rules {
		var signal TriggerSignal
		switch {
		case event.Type == events.TypeClusterDisconnected && (rule.Name == "agent_disconnected" || rule.Name == "agent_flapping"):
			signal = agentTriggerSignal(rule.Name, clusterID, payload.AgentVersion, "disconnected", "transport_disconnected", event.Time)
		case event.Type == events.TypeAgentFailed && rule.Name == "agent_auth_registration_failure":
			signal = agentTriggerSignal(rule.Name, clusterID, payload.AgentVersion, "auth_failed", "authentication_or_registration_failed", event.Time)
		default:
			continue
		}
		_, _, _ = r.ingestor.Ingest(ctx, rule, TriggerObservation{Signal: signal, Connections: observations, OriginResourceRef: "agent-connection:" + clusterID.String(), OriginEventRef: "agent-event:" + recorded.ID.String()})
	}
	return nil
}

type managementAlertTriggerQueries interface {
	GetAlertEventByID(context.Context, uuid.UUID) (sqlc.AlertEvent, error)
	GetAlertRuleByID(context.Context, uuid.UUID) (sqlc.AlertRule, error)
}

type changedEventPayload struct {
	ID        string `json:"id"`
	ClusterID string `json:"cluster_id"`
	Kind      string `json:"kind"`
}

// consumeManagementAlert intentionally accepts only unscoped Astronomer
// management-plane alerts. A cluster-scoped alert belongs to a downstream
// managed target and cannot silently widen Charlie v1 scope.
func (r *EventRuntime) consumeManagementAlert(ctx context.Context, event events.Event) error {
	queries, ok := r.queries.(managementAlertTriggerQueries)
	if !ok || !r.active() {
		return nil
	}
	raw, err := json.Marshal(event.Data)
	if err != nil || len(raw) > 4096 {
		return fmt.Errorf("Charlie alert event is invalid")
	}
	var payload changedEventPayload
	if json.Unmarshal(raw, &payload) != nil || payload.Kind != "event" || payload.ID == "" || payload.ClusterID != "" {
		return nil
	}
	eventID, err := uuid.Parse(payload.ID)
	if err != nil {
		return fmt.Errorf("Charlie alert identifier is invalid")
	}
	alert, err := queries.GetAlertEventByID(ctx, eventID)
	if err != nil || alert.ClusterID.Valid || (alert.Status != "firing" && alert.Status != "active") {
		return nil
	}
	rule, err := queries.GetAlertRuleByID(ctx, alert.RuleID)
	if err != nil || rule.ClusterID.Valid || (rule.Severity != "high" && rule.Severity != "critical") {
		return nil
	}
	connection, err := r.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return nil
	}
	rules, err := r.queries.ListEnabledCharlieTriggerRules(ctx, connection.ID)
	if err != nil {
		return err
	}
	for _, triggerRule := range rules {
		if triggerRule.Name != "alert_high_critical" {
			continue
		}
		signal := TriggerSignal{
			Source: "astronomer", EventType: triggerRule.Name, ResourceType: "alert", ResourceID: alert.ID.String(),
			FailureClass: "management_alert_firing", Severity: rule.Severity, State: alert.Status,
			Timestamp: alert.FiredAt.UTC(), ProductVersion: version.Version,
			Summary: "A high-severity Astronomer management-plane alert requires investigation",
		}
		_, _, err = r.ingestor.Ingest(ctx, triggerRule, TriggerObservation{
			Signal: signal, OriginResourceRef: "alert:" + alert.ID.String(), OriginEventRef: "alert-event:" + alert.ID.String(),
		})
		return err
	}
	return nil
}

type agentEventPayload struct {
	ClusterID    string `json:"cluster_id"`
	SessionID    string `json:"session_id"`
	AgentVersion string `json:"agent_version"`
}

func boundedAgentEventPayload(value any) (agentEventPayload, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 4096 {
		return agentEventPayload{}, fmt.Errorf("Charlie agent event is invalid")
	}
	var payload agentEventPayload
	if err := json.Unmarshal(raw, &payload); err != nil || strings.TrimSpace(payload.ClusterID) == "" || len(payload.AgentVersion) > 64 || len(payload.SessionID) > 128 {
		return agentEventPayload{}, fmt.Errorf("Charlie agent event is invalid")
	}
	return payload, nil
}

func productAgentEventType(value events.Type) (string, string) {
	switch value {
	case events.TypeClusterConnected:
		return "connected", ""
	case events.TypeAgentReconnecting:
		return "reconnecting", ""
	case events.TypeAgentFailed:
		return "auth_failed", "connection_rejected"
	default:
		return "disconnected", "transport_closed"
	}
}

func agentTriggerSignal(name string, clusterID uuid.UUID, agentVersion, state, failure string, at time.Time) TriggerSignal {
	severity := "warning"
	if name == "agent_auth_registration_failure" {
		severity = "critical"
	}
	return TriggerSignal{
		Source: "astronomer", EventType: name, ResourceType: "agent_connection_record", ResourceID: clusterID.String(),
		FailureClass: failure, Severity: severity, State: state, Timestamp: at.UTC(), ProductVersion: version.Version,
		Summary: "Astronomer cluster-agent connection state requires investigation",
	}
}
