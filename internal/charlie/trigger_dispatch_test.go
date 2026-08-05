package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type triggerDispatchFake struct {
	connection  sqlc.CharlieConnection
	rule        sqlc.CharlieTriggerRule
	event       sqlc.CharlieTriggerEvent
	session     sqlc.CharlieSession
	sessionErr  error
	created     int
	resources   int
	delegations int
	transitions []sqlc.TransitionCharlieTriggerEventParams
}

func (f *triggerDispatchFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *triggerDispatchFake) ClaimCharlieTriggerEvent(context.Context, uuid.UUID) (sqlc.CharlieTriggerEvent, error) {
	if f.event.State != "pending" && f.event.State != "retry" {
		return sqlc.CharlieTriggerEvent{}, pgx.ErrNoRows
	}
	f.event.State = "dispatching"
	f.event.AttemptCount++
	return f.event, nil
}
func (f *triggerDispatchFake) GetCharlieTriggerRule(context.Context, uuid.UUID) (sqlc.CharlieTriggerRule, error) {
	return f.rule, nil
}
func (f *triggerDispatchFake) GetCharlieSessionByClientID(context.Context, sqlc.GetCharlieSessionByClientIDParams) (sqlc.CharlieSession, error) {
	return f.session, f.sessionErr
}
func (f *triggerDispatchFake) CreateCharlieSession(_ context.Context, p sqlc.CreateCharlieSessionParams) (sqlc.CharlieSession, error) {
	f.created++
	f.sessionErr = nil
	f.session = sqlc.CharlieSession{ID: uuid.New(), ConnectionID: p.ConnectionID, ClientSessionID: p.ClientSessionID, Source: p.Source, Visibility: p.Visibility, State: p.State}
	return f.session, nil
}
func (f *triggerDispatchFake) AddCharlieSessionResource(context.Context, sqlc.AddCharlieSessionResourceParams) error {
	f.resources++
	return nil
}
func (f *triggerDispatchFake) CreateCharlieDelegation(_ context.Context, p sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.delegations++
	return sqlc.CharlieDelegation{SessionID: p.SessionID}, nil
}
func (f *triggerDispatchFake) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, pgx.ErrNoRows
}
func (f *triggerDispatchFake) BindCharlieSessionCentralID(_ context.Context, p sqlc.BindCharlieSessionCentralIDParams) (sqlc.CharlieSession, error) {
	f.session.CharlieSessionID = p.CharlieSessionID
	f.session.CentralRevision = p.CentralRevision
	f.session.State = "active"
	return f.session, nil
}
func (f *triggerDispatchFake) TransitionCharlieTriggerEvent(_ context.Context, p sqlc.TransitionCharlieTriggerEventParams) (sqlc.CharlieTriggerEvent, error) {
	f.transitions = append(f.transitions, p)
	f.event.State = p.NextState
	f.event.LastErrorCode = p.LastErrorCode
	f.event.SessionID = p.SessionID
	return f.event, nil
}

type investigationBridgeFake struct {
	request BridgeInvestigationRequest
	key     string
	receipt BridgeSessionReceipt
	err     error
	calls   int
}

func (f *investigationBridgeFake) CreateInvestigation(_ context.Context, request BridgeInvestigationRequest, key string) (BridgeSessionReceipt, error) {
	f.calls++
	f.request, f.key = request, key
	return f.receipt, f.err
}

type triggerPublisherFake struct{ states []string }

func (f *triggerPublisherFake) PublishCharlieTriggerLifecycle(_ context.Context, _ uuid.UUID, state, _ string) {
	f.states = append(f.states, state)
}

func triggerDispatchFixture() (*triggerDispatchFake, *investigationBridgeFake, *triggerPublisherFake) {
	connection := readySessionConnection()
	ruleID, eventID := uuid.New(), uuid.New()
	store := &triggerDispatchFake{
		connection: connection,
		rule: sqlc.CharlieTriggerRule{
			ID: ruleID, ConnectionID: connection.ID, Name: "agent_flapping", Enabled: true,
			WindowSeconds: 900, CooldownSeconds: 1800, MinimumSeverity: "warning",
			ModeCeiling: string(ModeReadOnly), ServiceIdentityID: uuid.New(),
		},
		event: sqlc.CharlieTriggerEvent{
			ID: eventID, RuleID: ruleID, Source: "astronomer", EventType: "agent_flapping",
			ResourceType: "agent_connection_record", ResourceID: "connection-1", Fingerprint: "fingerprint",
			SummaryMetadata: []byte(`{"severity":"warning"}`), State: "pending", RepeatCount: 4,
			FirstOccurredAt: time.Unix(100, 0), LastOccurredAt: time.Unix(200, 0),
		},
		sessionErr: pgx.ErrNoRows,
	}
	return store, &investigationBridgeFake{receipt: BridgeSessionReceipt{SessionID: "central-session", Revision: 3}}, &triggerPublisherFake{}
}

func TestTriggerDispatchCreatesOneIncidentSessionAndPublishesAfterCommit(t *testing.T) {
	store, bridge, publisher := triggerDispatchFixture()
	dispatcher, _ := NewTriggerDispatcher(store, bridge, publisher, func() bool { return true })
	dispatcher.now = func() time.Time { return time.Unix(1000, 0) }
	if err := dispatcher.Dispatch(context.Background(), store.event.ID); err != nil {
		t.Fatal(err)
	}
	if store.created != 1 || store.resources != 1 || store.delegations != 1 || bridge.calls != 1 {
		t.Fatalf("incident creation incomplete: store=%#v bridge=%#v", store, bridge)
	}
	if store.session.Source != "event" || store.session.Visibility != "incident" || bridge.key != store.event.ID.String() || bridge.request.AuthorizationRef == "" {
		t.Fatalf("incident binding was not stable/bounded: session=%#v request=%#v", store.session, bridge.request)
	}
	if len(store.transitions) != 1 || store.transitions[0].NextState != "dispatched" || !store.transitions[0].SessionID.Valid || len(publisher.states) != 1 || publisher.states[0] != "dispatched" {
		t.Fatalf("durable transition was not published after commit: transitions=%#v published=%#v", store.transitions, publisher.states)
	}
}

func TestTriggerDispatchRetryAndDeadLetterAreBounded(t *testing.T) {
	store, bridge, publisher := triggerDispatchFixture()
	store.rule.Thresholds = json.RawMessage(`{"maximum_attempts":2,"dead_letter_enabled":true}`)
	bridge.err = errors.New("central unavailable")
	dispatcher, _ := NewTriggerDispatcher(store, bridge, publisher, func() bool { return true })
	dispatcher.now = func() time.Time { return time.Unix(1000, 0) }
	if err := dispatcher.Dispatch(context.Background(), store.event.ID); err == nil || store.event.State != "retry" || store.event.LastErrorCode != "bridge_unavailable" {
		t.Fatalf("transient failure not scheduled: event=%#v err=%v", store.event, err)
	}
	store.event.State = "retry"
	store.event.AttemptCount = 1
	store.session = sqlc.CharlieSession{}
	store.sessionErr = pgx.ErrNoRows
	if err := dispatcher.Dispatch(context.Background(), store.event.ID); err != nil || store.event.State != "dead" {
		t.Fatalf("exhausted work not dead-lettered: event=%#v err=%v", store.event, err)
	}
	if publisher.states[len(publisher.states)-1] != "dead" {
		t.Fatal("operator lifecycle did not receive durable dead-letter state")
	}
}

func TestTriggerDispatchInactiveOrReplayDoesNothing(t *testing.T) {
	store, bridge, publisher := triggerDispatchFixture()
	dispatcher, _ := NewTriggerDispatcher(store, bridge, publisher, func() bool { return false })
	if err := dispatcher.Dispatch(context.Background(), store.event.ID); err != nil || bridge.calls != 0 || store.created != 0 {
		t.Fatal("inactive trigger contacted bridge")
	}
	dispatcher.active = func() bool { return true }
	store.event.State = "dispatched"
	if err := dispatcher.Dispatch(context.Background(), store.event.ID); err != nil || bridge.calls != 0 {
		t.Fatal("delivered outbox replay duplicated investigation")
	}
}

func TestTriggerDispatchNeverRevivesTerminalOrAmbiguousSession(t *testing.T) {
	for _, state := range []string{"aborted", "failed", "active"} {
		t.Run(state, func(t *testing.T) {
			store, bridge, publisher := triggerDispatchFixture()
			store.sessionErr = nil
			store.session = sqlc.CharlieSession{
				ID: uuid.New(), ConnectionID: store.connection.ID,
				ClientSessionID: store.event.ID, Source: "event",
				Visibility: "incident", State: state,
			}
			dispatcher, _ := NewTriggerDispatcher(store, bridge, publisher, func() bool { return true })
			if err := dispatcher.Dispatch(context.Background(), store.event.ID); err != nil {
				t.Fatal(err)
			}
			if bridge.calls != 0 || store.delegations != 0 || store.event.State != "dead" || store.event.LastErrorCode != "local_session_terminal" {
				t.Fatalf("terminal/ambiguous session was revived: state=%q store=%#v bridge_calls=%d", state, store, bridge.calls)
			}
		})
	}
}
