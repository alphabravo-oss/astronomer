package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type sessionAccessQueriesFake struct {
	connection sqlc.CharlieConnection
	session    sqlc.CharlieSession
	candidates []sqlc.CharlieSession
	resources  []sqlc.CharlieSessionResource
	created    []sqlc.CreateCharlieDelegationParams
	aborted    int
	revoked    int
	updates    []sqlc.UpdateCharlieSessionCursorParams
	updateErr  error
}

func (f *sessionAccessQueriesFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *sessionAccessQueriesFake) GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	return f.session, nil
}
func (f *sessionAccessQueriesFake) ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	return f.resources, nil
}
func (f *sessionAccessQueriesFake) ListCharlieSessionResourcesBatch(_ context.Context, sessionIDs []uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	allowed := make(map[uuid.UUID]struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		allowed[sessionID] = struct{}{}
	}
	result := make([]sqlc.CharlieSessionResource, 0, len(f.resources))
	for _, resource := range f.resources {
		if _, ok := allowed[resource.SessionID]; ok {
			result = append(result, resource)
		}
	}
	return result, nil
}
func (f *sessionAccessQueriesFake) ListCharlieAccessibleSessionCandidates(_ context.Context, params sqlc.ListCharlieAccessibleSessionCandidatesParams) ([]sqlc.CharlieSession, error) {
	rows := f.candidates
	if rows == nil {
		rows = []sqlc.CharlieSession{f.session}
	}
	start := int(params.PageOffset)
	if start >= len(rows) {
		return nil, nil
	}
	end := min(len(rows), start+int(params.PageLimit))
	return rows[start:end], nil
}
func (f *sessionAccessQueriesFake) ListCharlieSessionsForOwner(context.Context, sqlc.ListCharlieSessionsForOwnerParams) ([]sqlc.CharlieSession, error) {
	return []sqlc.CharlieSession{f.session}, nil
}
func (f *sessionAccessQueriesFake) CreateCharlieDelegation(_ context.Context, p sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.created = append(f.created, p)
	return sqlc.CharlieDelegation{SessionID: p.SessionID}, nil
}
func (f *sessionAccessQueriesFake) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, pgx.ErrNoRows
}
func (f *sessionAccessQueriesFake) AbortCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	f.aborted++
	f.session.State = "aborted"
	return f.session, nil
}
func (f *sessionAccessQueriesFake) RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error) {
	f.revoked++
	return 1, nil
}
func (f *sessionAccessQueriesFake) UpdateCharlieSessionCursor(_ context.Context, p sqlc.UpdateCharlieSessionCursorParams) (sqlc.CharlieSession, error) {
	if f.updateErr != nil {
		return sqlc.CharlieSession{}, f.updateErr
	}
	f.updates = append(f.updates, p)
	f.session.LastEventID = p.LastEventID
	return f.session, nil
}

type sessionAuthorizerFake struct {
	use               bool
	incident          bool
	incidentResources map[string]bool
	calls             int
}

func (f *sessionAuthorizerFake) CanUseCharlie(context.Context, uuid.UUID) (bool, error) {
	f.calls++
	return f.use, nil
}
func (f *sessionAuthorizerFake) CanReadIncidentResources(_ context.Context, _ uuid.UUID, resources []sqlc.CharlieSessionResource) (bool, error) {
	f.calls++
	if f.incidentResources != nil {
		if len(resources) == 0 {
			return false, nil
		}
		for _, resource := range resources {
			if !f.incidentResources[resource.ResourceID] {
				return false, nil
			}
		}
		return true, nil
	}
	return f.incident, nil
}

type contentBridgeFake struct {
	getCalls     int
	historyCalls int
	messageCalls int
	abortCalls   int
	message      string
	messageID    uuid.UUID
	err          error
	events       []contract.Event
	streamCursor string
}

func (f *contentBridgeFake) GetSession(context.Context, string, string) (json.RawMessage, error) {
	f.getCalls++
	return json.RawMessage(`{"state":"open"}`), f.err
}
func (f *contentBridgeFake) GetHistory(context.Context, string, string, string, int) (json.RawMessage, error) {
	f.historyCalls++
	return json.RawMessage(`[{"redacted_content":"answer"}]`), f.err
}
func (f *contentBridgeFake) CreateMessage(_ context.Context, _ string, _ string, messageID uuid.UUID, message string) (json.RawMessage, error) {
	f.messageCalls++
	f.message, f.messageID = message, messageID
	return json.RawMessage(`{"turn_id":"turn-1"}`), f.err
}
func (f *contentBridgeFake) AbortSession(context.Context, string, string, uuid.UUID) error {
	f.abortCalls++
	return f.err
}
func (f *contentBridgeFake) StreamSessionEvents(_ context.Context, _, _, cursor string, handle func(contract.Event) error) error {
	f.streamCursor = cursor
	for _, event := range f.events {
		if err := handle(event); err != nil {
			return err
		}
	}
	return f.err
}

type sessionAuditFake struct {
	events []SessionLifecycleAudit
	err    error
}

func (f *sessionAuditFake) RecordCharlieSessionLifecycle(_ context.Context, event SessionLifecycleAudit) {
	f.events = append(f.events, event)
}
func (f *sessionAuditFake) RecordCharlieAuthorityMutation(context.Context, AuthorityMutationAudit) error {
	return f.err
}

func readyPrivateAccess(owner uuid.UUID) (*sessionAccessQueriesFake, uuid.UUID) {
	connection := readySessionConnection()
	sessionID := uuid.New()
	return &sessionAccessQueriesFake{
		connection: connection,
		session: sqlc.CharlieSession{
			ID: sessionID, ConnectionID: connection.ID, CharlieSessionID: "central-1", ClientSessionID: uuid.New(),
			OwnerUserID: pgtype.UUID{Bytes: owner, Valid: true}, Source: "user", Visibility: "private", State: "active",
		},
		resources: []sqlc.CharlieSessionResource{{SessionID: sessionID, ResourceType: "installation", ResourceID: "local", RequiredVerb: "read"}},
	}, sessionID
}

func TestPrivateSessionOwnerIsolationAndLiveRecheck(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	authorizer := &sessionAuthorizerFake{use: true}
	bridge := &contentBridgeFake{}
	audit := &sessionAuditFake{}
	service, err := NewSessionAccessService(queries, authorizer, bridge, audit, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Get(context.Background(), uuid.New(), sessionID); err == nil {
		t.Fatal("owner B read owner A's private Charlie session")
	}
	if bridge.getCalls != 0 || len(queries.created) != 0 {
		t.Fatal("denied owner access reached bridge or minted authority")
	}
	if _, err := service.Get(context.Background(), owner, sessionID); err != nil {
		t.Fatal(err)
	}
	if bridge.getCalls != 1 || len(queries.created) != 1 {
		t.Fatal("owner access did not use a fresh hash-only delegation")
	}
	authorizer.use = false
	if _, err := service.History(context.Background(), owner, sessionID, "", 20); err == nil {
		t.Fatal("revoked live binding still permitted history")
	}
	if bridge.historyCalls != 0 {
		t.Fatal("revoked binding reached Charlie content")
	}
}

func TestSessionAccessAuditFailureMintsNoDelegation(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	bridge := &contentBridgeFake{}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{err: errors.New("database-SENTINEL")}, func() bool { return true })

	if _, err := service.Get(context.Background(), owner, sessionID); err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("session delegation audit failure was not bounded: %v", err)
	}
	if len(queries.created) != 0 || bridge.getCalls != 0 {
		t.Fatalf("audit failure minted session authority: delegations=%d bridge=%d", len(queries.created), bridge.getCalls)
	}
}

func TestCurrentModeUsesLiveUserAndEffectiveConnectionAuthority(t *testing.T) {
	actor := uuid.New()
	queries, _ := readyPrivateAccess(actor)
	queries.connection.RequestedMode = string(ModeAuto)
	queries.connection.VerifiedMode = string(ModeApproval)
	authorizer := &sessionAuthorizerFake{use: true}
	service, err := NewSessionAccessService(queries, authorizer, &contentBridgeFake{}, &sessionAuditFake{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	mode, err := service.CurrentMode(context.Background(), actor)
	if err != nil || mode != ModeApproval {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	authorizer.use = false
	if _, err := service.CurrentMode(context.Background(), actor); err == nil {
		t.Fatal("revoked Charlie access still disclosed the current mode")
	}
}

func TestListAccessiblePreservesPrivateOwnershipAndFiltersEveryIncidentResource(t *testing.T) {
	actor := uuid.New()
	queries, _ := readyPrivateAccess(actor)
	connectionID := queries.connection.ID
	ownPrivate := queries.session
	foreignPrivate := ownPrivate
	foreignPrivate.ID = uuid.New()
	foreignPrivate.OwnerUserID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	allowedIncident := ownPrivate
	allowedIncident.ID = uuid.New()
	allowedIncident.OwnerUserID = pgtype.UUID{}
	allowedIncident.Source = "event"
	allowedIncident.Visibility = "incident"
	deniedIncident := allowedIncident
	deniedIncident.ID = uuid.New()
	queries.candidates = []sqlc.CharlieSession{ownPrivate, foreignPrivate, allowedIncident, deniedIncident}
	queries.resources = []sqlc.CharlieSessionResource{
		{SessionID: allowedIncident.ID, ResourceType: "agent_connection_record", ResourceID: "agent-a", RequiredVerb: "read"},
		{SessionID: allowedIncident.ID, ResourceType: "alert", ResourceID: "alert-a", RequiredVerb: "read"},
		{SessionID: deniedIncident.ID, ResourceType: "agent_connection_record", ResourceID: "agent-a", RequiredVerb: "read"},
		{SessionID: deniedIncident.ID, ResourceType: "alert", ResourceID: "alert-denied", RequiredVerb: "read"},
	}
	authorizer := &sessionAuthorizerFake{use: true, incidentResources: map[string]bool{
		"agent-a": true,
		"alert-a": true,
	}}
	service, err := NewSessionAccessService(queries, authorizer, &contentBridgeFake{}, &sessionAuditFake{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.ListAccessible(context.Background(), actor, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != ownPrivate.ID || rows[1].ID != allowedIncident.ID {
		t.Fatalf("accessible rows=%#v connection=%s", rows, connectionID)
	}
	rows, err = service.ListAccessible(context.Background(), actor, 1, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != allowedIncident.ID {
		t.Fatalf("accessible offset rows=%#v err=%v", rows, err)
	}
}

func TestIncidentSessionChecksAffectedResourcesEveryCall(t *testing.T) {
	actor := uuid.New()
	queries, sessionID := readyPrivateAccess(uuid.New())
	queries.session.Source = "event"
	queries.session.Visibility = "incident"
	queries.session.OwnerUserID = pgtype.UUID{}
	authorizer := &sessionAuthorizerFake{use: true, incident: false}
	bridge := &contentBridgeFake{}
	service, _ := NewSessionAccessService(queries, authorizer, bridge, &sessionAuditFake{}, func() bool { return true })

	if _, err := service.History(context.Background(), actor, sessionID, "", 10); err == nil {
		t.Fatal("unrelated operator read shared incident")
	}
	authorizer.incident = true
	if _, err := service.History(context.Background(), actor, sessionID, "", 10); err != nil {
		t.Fatal(err)
	}
	authorizer.incident = false
	if _, err := service.Get(context.Background(), actor, sessionID); err == nil {
		t.Fatal("resource permission revocation was not rechecked")
	}
	if bridge.historyCalls != 1 || bridge.getCalls != 0 {
		t.Fatalf("unexpected bridge calls: history=%d get=%d", bridge.historyCalls, bridge.getCalls)
	}
}

func TestMessageUsesStableClientIDWithoutLocalContentPersistence(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	bridge := &contentBridgeFake{}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{}, func() bool { return true })
	messageID := uuid.New()
	message := "diagnose the management plane"

	first, err := service.Message(context.Background(), owner, sessionID, messageID, message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Message(context.Background(), owner, sessionID, messageID, message)
	if err != nil {
		t.Fatal(err)
	}
	if bridge.messageCalls != 2 || bridge.messageID != messageID || bridge.message != message || string(first) != string(second) {
		t.Fatal("stable client message identity was not proxied exactly")
	}
	if len(queries.created) != 2 {
		t.Fatal("each live request must receive a fresh expiring authorization reference")
	}
}

func TestAbortRevokesLocallyBeforeRemoteAndDoesNotReopen(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	bridge := &contentBridgeFake{err: errors.New("central unavailable")}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{}, func() bool { return true })

	err := service.Abort(context.Background(), owner, sessionID, uuid.New())
	if err == nil || queries.aborted != 1 || queries.revoked != 1 || bridge.abortCalls != 1 || queries.session.State != "aborted" {
		t.Fatalf("local abort did not fail closed before remote: err=%v queries=%#v bridge=%#v", err, queries, bridge)
	}
	if _, err := service.Get(context.Background(), owner, sessionID); err == nil {
		t.Fatal("locally aborted session became usable while central was unavailable")
	}
}

func TestAbortClosesCompletedSessionAndRevokesDelegations(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	queries.session.State = "completed"
	bridge := &contentBridgeFake{}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{}, func() bool { return true })

	if err := service.Abort(context.Background(), owner, sessionID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if queries.aborted != 1 || queries.revoked != 1 || bridge.abortCalls != 1 || queries.session.State != "aborted" {
		t.Fatalf("completed session was not closed exactly once: queries=%#v bridge=%#v", queries, bridge)
	}
	if _, err := service.Get(context.Background(), owner, sessionID); err == nil {
		t.Fatal("closed completed session retained product access")
	}
}

func TestInactiveSessionAPIIsSilent(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	bridge := &contentBridgeFake{}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true, incident: true}, bridge, &sessionAuditFake{}, func() bool { return false })
	if _, err := service.Get(context.Background(), owner, sessionID); err == nil {
		t.Fatal("inactive integration allowed a session read")
	}
	if bridge.getCalls != 0 || len(queries.created) != 0 {
		t.Fatal("inactive integration caused bridge or persistence activity")
	}
}

func TestStreamPersistsCursorOnlyAfterBrowserAcknowledgement(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	queries.session.LastEventID = "event-0"
	bridge := &contentBridgeFake{events: []contract.Event{{ID: "event-1", Event: "message.delta", Data: []byte(`{"text":"hello"}`)}}}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{}, func() bool { return true })
	handled := false
	if err := service.Stream(context.Background(), owner, sessionID, "", func(event contract.Event) error {
		if len(queries.updates) != 0 {
			t.Fatal("cursor persisted before browser callback")
		}
		handled = event.ID == "event-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !handled || bridge.streamCursor != "event-0" || len(queries.updates) != 1 || queries.updates[0].LastEventID != "event-1" {
		t.Fatalf("stream acknowledgement mismatch handled=%v cursor=%q updates=%#v", handled, bridge.streamCursor, queries.updates)
	}
}

func TestStreamRechecksLiveAuthorizationBeforeEveryEvent(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	authorizer := &sessionAuthorizerFake{use: true}
	bridge := &contentBridgeFake{events: []contract.Event{{ID: "event-1"}, {ID: "event-2"}}}
	service, _ := NewSessionAccessService(queries, authorizer, bridge, &sessionAuditFake{}, func() bool { return true })
	delivered := 0
	err := service.Stream(context.Background(), owner, sessionID, "browser-event", func(contract.Event) error {
		delivered++
		authorizer.use = false
		return nil
	})
	if err == nil || delivered != 1 || len(queries.updates) != 1 || bridge.streamCursor != "browser-event" {
		t.Fatalf("revoked stream remained active err=%v delivered=%d updates=%d cursor=%q", err, delivered, len(queries.updates), bridge.streamCursor)
	}
}

func TestStreamDoesNotAcknowledgeFailedBrowserWrite(t *testing.T) {
	owner := uuid.New()
	queries, sessionID := readyPrivateAccess(owner)
	bridge := &contentBridgeFake{events: []contract.Event{{ID: "event-1"}}}
	service, _ := NewSessionAccessService(queries, &sessionAuthorizerFake{use: true}, bridge, &sessionAuditFake{}, func() bool { return true })
	err := service.Stream(context.Background(), owner, sessionID, "", func(contract.Event) error { return errors.New("browser disconnected") })
	if err == nil || len(queries.updates) != 0 {
		t.Fatalf("failed write acknowledged err=%v updates=%#v", err, queries.updates)
	}
}

func TestStreamMapsOnlyBoundedLifecycleMetadata(t *testing.T) {
	current := sqlc.CharlieSession{State: "active", CentralRevision: 3}
	now := time.Unix(200000, 0).UTC()
	state, revision, completed := sessionCursorState(current, contract.Event{ID: "9", Event: "permission.requested", Data: []byte(`{"prompt":"must-not-persist"}`)}, now)
	if state != "waiting_approval" || revision != 9 || completed.Valid {
		t.Fatalf("approval cursor metadata=%q/%d/%#v", state, revision, completed)
	}
	state, revision, completed = sessionCursorState(current, contract.Event{ID: "10", Event: "turn.completed", Data: []byte(`{"answer":"must-not-persist"}`)}, now)
	if state != "completed" || revision != 10 || !completed.Valid || !completed.Time.Equal(now) {
		t.Fatalf("terminal cursor metadata=%q/%d/%#v", state, revision, completed)
	}
}
