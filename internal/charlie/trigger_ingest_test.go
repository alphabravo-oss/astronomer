package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type triggerIngestFake struct {
	created    []sqlc.CreateCharlieTriggerEventWithOutboxParams
	suppressed []sqlc.SuppressActiveCharlieTriggerEventParams
	repeat     int32
}

func (f *triggerIngestFake) CreateCharlieTriggerEventWithOutbox(_ context.Context, p sqlc.CreateCharlieTriggerEventWithOutboxParams) (sqlc.CreateCharlieTriggerEventWithOutboxRow, error) {
	f.created = append(f.created, p)
	f.repeat++
	return sqlc.CreateCharlieTriggerEventWithOutboxRow{ID: uuid.New(), RuleID: p.RuleID, Fingerprint: p.Fingerprint, RepeatCount: f.repeat, FirstOccurredAt: p.OccurredAt, LastOccurredAt: p.OccurredAt}, nil
}
func (f *triggerIngestFake) SuppressActiveCharlieTriggerEvent(_ context.Context, p sqlc.SuppressActiveCharlieTriggerEventParams) (sqlc.CharlieTriggerEvent, error) {
	f.suppressed = append(f.suppressed, p)
	return sqlc.CharlieTriggerEvent{RuleID: p.RuleID, Fingerprint: p.Fingerprint, State: "suppressed"}, nil
}

func triggerRule(name string, window time.Duration) sqlc.CharlieTriggerRule {
	return sqlc.CharlieTriggerRule{
		ID: uuid.New(), Name: name, Enabled: true, MinimumSeverity: "warning",
		WindowSeconds: int32(window / time.Second), CooldownSeconds: int32(DefaultTriggerCooldown / time.Second),
		ModeCeiling: string(ModeReadOnly), Thresholds: json.RawMessage(`{"count":3}`),
	}
}

func triggerSignal(name string, occurred time.Time) TriggerSignal {
	return TriggerSignal{
		Source: "astronomer", EventType: name, ResourceType: "agent_connection_record", ResourceID: "connection-1",
		FailureClass: "transport_disconnected", Severity: "warning", State: "disconnected", Timestamp: occurred,
		ProductVersion: "1.2.3", Environment: "production", Region: "us-east", Summary: "agent token=secret disconnected",
	}
}

func TestDisconnectGraceIsDurableAndReconnectCancelsBeforeDispatch(t *testing.T) {
	now := time.Unix(10000, 0).UTC()
	store := &triggerIngestFake{}
	ingestor, _ := NewTriggerIngestor(store, func() bool { return true })
	ingestor.now = func() time.Time { return now }
	rule := triggerRule("agent_disconnected", DefaultDisconnectGrace)
	signal := triggerSignal(rule.Name, now.Add(-time.Minute))

	row, created, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{
		Signal: signal, Connections: []ConnectionObservation{{State: "disconnected", OccurredAt: signal.Timestamp}},
		OriginResourceRef: "agent-connection:connection-1", OriginEventRef: "audit:event-1",
	})
	if err != nil || !created || row.ID == uuid.Nil || len(store.created) != 1 {
		t.Fatalf("disconnect was not durably scheduled: row=%#v created=%v err=%v", row, created, err)
	}
	if got, want := store.created[0].NextAttemptAt, signal.Timestamp.Add(DefaultDisconnectGrace); !got.Equal(want) {
		t.Fatalf("dispatch_at=%s want=%s", got, want)
	}
	if string(store.created[0].SummaryMetadata) == "" || containsSecret(string(store.created[0].SummaryMetadata)) {
		t.Fatalf("summary metadata was not redacted: %s", store.created[0].SummaryMetadata)
	}
	cancelled, err := ingestor.CancelDisconnected(context.Background(), rule, signal)
	if err != nil || !cancelled || len(store.suppressed) != 1 || store.suppressed[0].ReasonCode != "reconnected_within_grace" {
		t.Fatalf("reconnect did not suppress due work: cancelled=%v err=%v", cancelled, err)
	}
}

func TestFlappingRequiresExactlyConfiguredEventsInsideWindow(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	store := &triggerIngestFake{}
	ingestor, _ := NewTriggerIngestor(store, func() bool { return true })
	ingestor.now = func() time.Time { return now }
	rule := triggerRule("agent_flapping", DefaultFlapWindow)
	signal := triggerSignal(rule.Name, now)
	observations := []ConnectionObservation{
		{State: "disconnected", OccurredAt: now.Add(-14 * time.Minute)},
		{State: "disconnected", OccurredAt: now.Add(-8 * time.Minute)},
	}
	if _, created, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{Signal: signal, Connections: observations}); err != nil || created {
		t.Fatalf("two disconnects incorrectly triggered: created=%v err=%v", created, err)
	}
	observations = append(observations, ConnectionObservation{State: "disconnected", OccurredAt: now.Add(-time.Minute)})
	if _, created, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{Signal: signal, Connections: observations}); err != nil || !created {
		t.Fatalf("third disconnect did not trigger: created=%v err=%v", created, err)
	}
}

func TestDuplicateTriggerUsesOneStableFingerprintAndInactiveIsSilent(t *testing.T) {
	now := time.Unix(30000, 0).UTC()
	store := &triggerIngestFake{}
	active := true
	ingestor, _ := NewTriggerIngestor(store, func() bool { return active })
	ingestor.now = func() time.Time { return now }
	rule := triggerRule("alert_high_critical", time.Minute)
	signal := triggerSignal(rule.Name, now)
	signal.ResourceType = "alert"

	first, ok, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{Signal: signal})
	if err != nil || !ok {
		t.Fatal(err)
	}
	signal.Summary = "different prose must not change identity"
	second, ok, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{Signal: signal})
	if err != nil || !ok || first.Fingerprint != second.Fingerprint || second.RepeatCount != 2 {
		t.Fatalf("repeat did not coalesce: first=%#v second=%#v err=%v", first, second, err)
	}
	active = false
	if _, ok, err := ingestor.Ingest(context.Background(), rule, TriggerObservation{Signal: signal}); err != nil || ok || len(store.created) != 2 {
		t.Fatalf("inactive trigger was not silent: ok=%v err=%v", ok, err)
	}
}

func containsSecret(value string) bool {
	return value != "" && (strings.Contains(value, "token=secret") || strings.Contains(value, "secret"))
}
