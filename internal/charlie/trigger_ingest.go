package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type triggerIngestQueries interface {
	CreateCharlieTriggerEventWithOutbox(context.Context, sqlc.CreateCharlieTriggerEventWithOutboxParams) (sqlc.CreateCharlieTriggerEventWithOutboxRow, error)
	SuppressActiveCharlieTriggerEvent(context.Context, sqlc.SuppressActiveCharlieTriggerEventParams) (sqlc.CharlieTriggerEvent, error)
}

type TriggerObservation struct {
	Signal            TriggerSignal
	Connections       []ConnectionObservation
	OriginResourceRef string
	OriginEventRef    string
}

type TriggerIngestor struct {
	queries triggerIngestQueries
	active  func() bool
	now     func() time.Time
}

func NewTriggerIngestor(queries triggerIngestQueries, active func() bool) (*TriggerIngestor, error) {
	if queries == nil || active == nil {
		return nil, fmt.Errorf("Charlie trigger ingestion requires durable outbox storage and activation")
	}
	return &TriggerIngestor{queries: queries, active: active, now: time.Now}, nil
}

// Ingest evaluates product-owned rule policy and atomically persists both the
// deduplicated trigger event and its task-outbox intent. It never contacts
// Charlie and never grants action authority.
func (i *TriggerIngestor) Ingest(ctx context.Context, rule sqlc.CharlieTriggerRule, observation TriggerObservation) (sqlc.CreateCharlieTriggerEventWithOutboxRow, bool, error) {
	if !i.active() {
		observeTrigger(rule.Name, "inactive")
		return sqlc.CreateCharlieTriggerEventWithOutboxRow{}, false, nil
	}
	prepared, err := PrepareTrigger(observation.Signal)
	if err != nil || !validTriggerRule(rule) || rule.Name != observation.Signal.EventType || !severityAtLeast(observation.Signal.Severity, rule.MinimumSeverity) {
		observeTrigger(rule.Name, "filtered")
		return sqlc.CreateCharlieTriggerEventWithOutboxRow{}, false, fmt.Errorf("Charlie trigger does not match a valid enabled rule")
	}
	now := i.now().UTC()
	eligible, dispatchAt := evaluateTriggerRule(rule, observation, now)
	if !eligible {
		observeTrigger(rule.Name, "filtered")
		return sqlc.CreateCharlieTriggerEventWithOutboxRow{}, false, nil
	}
	metadata, err := boundedTriggerMetadata(prepared.Signal)
	if err != nil || len(observation.OriginResourceRef) > 255 || len(observation.OriginEventRef) > 255 {
		return sqlc.CreateCharlieTriggerEventWithOutboxRow{}, false, fmt.Errorf("Charlie trigger metadata is invalid")
	}
	row, err := i.queries.CreateCharlieTriggerEventWithOutbox(ctx, sqlc.CreateCharlieTriggerEventWithOutboxParams{
		RuleID: rule.ID, Source: prepared.Signal.Source, EventType: prepared.Signal.EventType,
		ResourceType: prepared.Signal.ResourceType, ResourceID: prepared.Signal.ResourceID,
		Fingerprint: prepared.Fingerprint, SummaryMetadata: metadata,
		NextAttemptAt: dispatchAt, OccurredAt: prepared.Signal.Timestamp.UTC(),
		OriginResourceRef: observation.OriginResourceRef, OriginEventRef: observation.OriginEventRef,
	})
	if err != nil {
		return sqlc.CreateCharlieTriggerEventWithOutboxRow{}, false, fmt.Errorf("persist Charlie trigger and outbox intent: %w", err)
	}
	outcome := "scheduled"
	if row.RepeatCount > 1 {
		outcome = "coalesced"
	}
	observeTrigger(rule.Name, outcome)
	return row, true, nil
}

// CancelDisconnected suppresses work that has not been dispatched when the
// product-owned connection state recovers inside the configured grace period.
func (i *TriggerIngestor) CancelDisconnected(ctx context.Context, rule sqlc.CharlieTriggerRule, original TriggerSignal) (bool, error) {
	if !i.active() || rule.Name != "agent_disconnected" {
		return false, nil
	}
	prepared, err := PrepareTrigger(original)
	if err != nil {
		return false, fmt.Errorf("Charlie disconnect recovery is invalid")
	}
	if _, err := i.queries.SuppressActiveCharlieTriggerEvent(ctx, sqlc.SuppressActiveCharlieTriggerEventParams{ReasonCode: "reconnected_within_grace", RuleID: rule.ID, Fingerprint: prepared.Fingerprint}); err != nil {
		return false, nil
	}
	observeTrigger(rule.Name, "suppressed")
	return true, nil
}

func validTriggerRule(rule sqlc.CharlieTriggerRule) bool {
	return rule.ID != [16]byte{} && rule.Enabled && rule.WindowSeconds >= 1 && rule.WindowSeconds <= 86400 &&
		rule.CooldownSeconds >= 0 && rule.CooldownSeconds <= 604800 &&
		(rule.ModeCeiling == string(ModeReadOnly) || rule.ModeCeiling == string(ModeApproval) || rule.ModeCeiling == string(ModeAuto))
}

func evaluateTriggerRule(rule sqlc.CharlieTriggerRule, observation TriggerObservation, now time.Time) (bool, time.Time) {
	window := time.Duration(rule.WindowSeconds) * time.Second
	switch rule.Name {
	case "agent_disconnected":
		if len(observation.Connections) == 0 {
			return false, time.Time{}
		}
		if !DisconnectedBeyondGrace(observation.Connections, now, window) {
			last := observation.Connections[0]
			for _, candidate := range observation.Connections[1:] {
				if candidate.OccurredAt.After(last.OccurredAt) {
					last = candidate
				}
			}
			if last.State != "disconnected" {
				return false, time.Time{}
			}
			return true, last.OccurredAt.UTC().Add(window)
		}
		return true, now
	case "agent_flapping":
		threshold := triggerThreshold(rule.Thresholds, "count", DefaultFlapCount)
		return FlappingInsideWindow(observation.Connections, now, window, threshold), now
	case "agent_auth_registration_failure":
		threshold := triggerThreshold(rule.Thresholds, "count", 3)
		return connectionEventsInsideWindow(observation.Connections, now, window, threshold, "auth_failed", "registration_failed"), now
	default:
		return true, now
	}
}

func connectionEventsInsideWindow(observations []ConnectionObservation, now time.Time, window time.Duration, threshold int, states ...string) bool {
	allowed := make(map[string]bool, len(states))
	for _, state := range states {
		allowed[state] = true
	}
	cutoff := now.Add(-window)
	count := 0
	for _, observation := range observations {
		if allowed[observation.State] && !observation.OccurredAt.Before(cutoff) && !observation.OccurredAt.After(now) {
			count++
		}
	}
	return count >= threshold
}

func triggerThreshold(raw json.RawMessage, name string, fallback int) int {
	var values map[string]int
	if json.Unmarshal(raw, &values) != nil || values[name] < 1 {
		return fallback
	}
	return values[name]
}

func boundedTriggerMetadata(signal TriggerSignal) (json.RawMessage, error) {
	value := map[string]string{
		"severity": signal.Severity, "state": signal.State,
		"product_version": signal.ProductVersion, "environment": signal.Environment,
		"region": signal.Region, "summary": signal.Summary,
	}
	for key, item := range value {
		item = strings.TrimSpace(item)
		if item == "" {
			delete(value, key)
			continue
		}
		if len(item) > maxTriggerSummaryBytes {
			return nil, fmt.Errorf("trigger metadata exceeds bound")
		}
		value[key] = item
	}
	return json.Marshal(value)
}

func severityAtLeast(value, minimum string) bool {
	// Accept Charlie's five-level vocabulary while preserving Astronomer's
	// existing "warning" level as the same rank as medium.
	rank := map[string]int{"info": 1, "low": 2, "medium": 3, "warning": 3, "high": 4, "critical": 5}
	return rank[value] > 0 && rank[value] >= rank[minimum]
}
