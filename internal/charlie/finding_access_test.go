package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type findingAccessFake struct {
	connection  sqlc.CharlieConnection
	rows        []sqlc.CharlieFinding
	resources   map[uuid.UUID][]sqlc.CharlieFindingResource
	session     sqlc.CharlieSession
	transition  sqlc.TransitionCharlieFindingParams
	delegations int
	lists       int
}

func (f *findingAccessFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *findingAccessFake) GetCharlieFinding(_ context.Context, id uuid.UUID) (sqlc.CharlieFinding, error) {
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return sqlc.CharlieFinding{}, pgx.ErrNoRows
}
func (f *findingAccessFake) GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	return f.session, nil
}
func (f *findingAccessFake) ListCharlieFindingResources(_ context.Context, id uuid.UUID) ([]sqlc.CharlieFindingResource, error) {
	return f.resources[id], nil
}
func (f *findingAccessFake) ListCharlieFindings(context.Context, sqlc.ListCharlieFindingsParams) ([]sqlc.CharlieFinding, error) {
	f.lists++
	return f.rows, nil
}
func (f *findingAccessFake) TransitionCharlieFinding(_ context.Context, p sqlc.TransitionCharlieFindingParams) (sqlc.CharlieFinding, error) {
	f.transition = p
	for i := range f.rows {
		if f.rows[i].ID == p.ID && f.rows[i].Status == p.ExpectedStatus {
			f.rows[i].Status = p.NextStatus
			return f.rows[i], nil
		}
	}
	return sqlc.CharlieFinding{}, pgx.ErrNoRows
}
func (f *findingAccessFake) CreateCharlieDelegation(_ context.Context, p sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.delegations++
	return sqlc.CharlieDelegation{SessionID: p.SessionID}, nil
}
func (f *findingAccessFake) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, pgx.ErrNoRows
}

type findingAccessAuthorizerFake struct {
	use          bool
	resourcePass map[string]bool
}

func (f *findingAccessAuthorizerFake) CanUseCharlie(context.Context, uuid.UUID) (bool, error) {
	return f.use, nil
}
func (f *findingAccessAuthorizerFake) CanReadIncidentResources(_ context.Context, _ uuid.UUID, resources []sqlc.CharlieSessionResource) (bool, error) {
	return len(resources) > 0 && f.resourcePass[resources[0].ResourceID], nil
}

type findingBridgeFake struct {
	getCalls        int
	transitionCalls int
	authRef         string
	next            string
}

func (f *findingBridgeFake) GetFinding(_ context.Context, _, auth string) (json.RawMessage, error) {
	f.getCalls++
	f.authRef = auth
	return json.RawMessage(`{"schema":"charlie.finding/v1"}`), nil
}
func (f *findingBridgeFake) TransitionFinding(_ context.Context, _, auth string, _ uuid.UUID, next string) (json.RawMessage, error) {
	f.transitionCalls++
	f.authRef, f.next = auth, next
	return json.RawMessage(`{"status":"acknowledged"}`), nil
}

type findingAuditFake struct{ entries []FindingLifecycleAudit }

func (f *findingAuditFake) RecordCharlieFindingLifecycle(_ context.Context, event FindingLifecycleAudit) {
	f.entries = append(f.entries, event)
}

type findingLifecyclePublisherFake struct{ alerts []FindingAlert }

func (f *findingLifecyclePublisherFake) PublishCharlieFindingLifecycle(_ context.Context, alert FindingAlert) {
	f.alerts = append(f.alerts, alert)
}

type findingSyncerFake struct {
	calls int
	err   error
}

func (f *findingSyncerFake) SyncForActor(context.Context, uuid.UUID) error {
	f.calls++
	return f.err
}

func findingAccessFixture() (*findingAccessFake, *findingAccessAuthorizerFake, *findingBridgeFake, *findingAuditFake, *findingLifecyclePublisherFake, uuid.UUID) {
	connection := readySessionConnection()
	actorID, sessionID, findingID := uuid.New(), uuid.New(), uuid.New()
	store := &findingAccessFake{
		connection: connection,
		rows:       []sqlc.CharlieFinding{{ID: findingID, ConnectionID: connection.ID, CharlieFindingID: "finding-central", SessionID: pgtype.UUID{Bytes: sessionID, Valid: true}, Severity: "warning", Status: "open", ExecutionBlockCode: "approval_required", RepeatCount: 2}},
		resources:  map[uuid.UUID][]sqlc.CharlieFindingResource{findingID: {{FindingID: findingID, ResourceType: "tunnel", ResourceID: "replica-a", RequiredVerb: "read"}}},
		session: sqlc.CharlieSession{
			ID: sessionID, ConnectionID: connection.ID, CharlieSessionID: "central-session",
			OwnerUserID: pgtype.UUID{Bytes: actorID, Valid: true}, Source: "user", Visibility: "private", State: "active",
		},
	}
	return store, &findingAccessAuthorizerFake{use: true, resourcePass: map[string]bool{"replica-a": true}}, &findingBridgeFake{}, &findingAuditFake{}, &findingLifecyclePublisherFake{}, actorID
}

func TestFindingAccessListFiltersEveryFindingByLiveResourceAuthorization(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	deniedID := uuid.New()
	store.rows = append(store.rows, sqlc.CharlieFinding{ID: deniedID, ConnectionID: store.connection.ID, Status: "open"})
	store.resources[deniedID] = []sqlc.CharlieFindingResource{{FindingID: deniedID, ResourceType: "tunnel", ResourceID: "replica-denied", RequiredVerb: "read"}}
	syncer := &findingSyncerFake{}
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, syncer, func() bool { return true })
	views, err := service.List(context.Background(), actorID, "open", 0, 20)
	if err != nil || len(views) != 1 || views[0].Finding.ID != store.rows[0].ID || bridge.getCalls != 0 || syncer.calls != 1 {
		t.Fatalf("finding list did not fail closed per resource: views=%#v err=%v", views, err)
	}
}

func TestFindingAccessFetchesCentralDetailOnlyAfterLiveAuthorization(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	view, err := service.Get(context.Background(), actorID, store.rows[0].ID)
	if err != nil || bridge.getCalls != 1 || bridge.authRef == "" || store.delegations != 1 || len(view.Remote) == 0 {
		t.Fatalf("central detail was not live-authorized: view=%#v err=%v", view, err)
	}
	authorizer.resourcePass["replica-a"] = false
	if _, err := service.Get(context.Background(), actorID, store.rows[0].ID); err == nil || bridge.getCalls != 1 {
		t.Fatal("revoked resource authorization still reached Charlie")
	}
}

func TestFindingAccessTransitionsCentralThenCommitsAndPublishes(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	view, err := service.Transition(context.Background(), actorID, store.rows[0].ID, uuid.New(), "acknowledged")
	if err != nil || bridge.transitionCalls != 1 || bridge.next != "acknowledged" || store.transition.ExpectedStatus != "open" || view.Finding.Status != "acknowledged" {
		t.Fatalf("finding transition incomplete: view=%#v transition=%#v err=%v", view, store.transition, err)
	}
	if len(publisher.alerts) != 1 || publisher.alerts[0].Severity != "medium" || publisher.alerts[0].ResourceID != "replica-a" {
		t.Fatalf("durable lifecycle did not publish bounded alert: %#v", publisher.alerts)
	}
}

func TestFindingAccessInactivePerformsNoDatabaseOrBridgeWork(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return false })
	if _, err := service.List(context.Background(), actorID, "", 0, 20); err == nil || store.lists != 0 || bridge.getCalls != 0 {
		t.Fatal("inactive finding surface performed work")
	}
}

func TestFindingAccessServesAuthorizedCachedRowsDuringCentralOutage(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	syncer := &findingSyncerFake{err: errors.New("central unavailable")}
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, syncer, func() bool { return true })
	views, err := service.List(context.Background(), actorID, "open", 0, 20)
	if err != nil || len(views) != 1 || syncer.calls != 1 {
		t.Fatalf("cached findings unavailable during central outage: views=%#v err=%v", views, err)
	}
}

func TestFindingAccessDoesNotLeakPrivateSessionFindingToResourceReader(t *testing.T) {
	store, authorizer, bridge, audit, publisher, _ := findingAccessFixture()
	otherActor := uuid.New()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	views, err := service.List(context.Background(), otherActor, "open", 0, 20)
	if err != nil || len(views) != 0 {
		t.Fatalf("private session finding leaked to another resource reader: views=%#v err=%v", views, err)
	}
}
