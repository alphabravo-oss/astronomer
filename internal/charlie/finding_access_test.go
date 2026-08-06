package charlie

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type findingAccessFake struct {
	mu          sync.Mutex
	connection  sqlc.CharlieConnection
	connections map[uuid.UUID]sqlc.CharlieConnection
	rows        []sqlc.CharlieFinding
	resources   map[uuid.UUID][]sqlc.CharlieFindingResource
	session     sqlc.CharlieSession
	transition  sqlc.TransitionCharlieFindingParams
	decisions   map[uuid.UUID]sqlc.CharlieFindingDecision
	delegations int
	lists       int
}

func (f *findingAccessFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connection, nil
}
func (f *findingAccessFake) GetCharlieConnection(_ context.Context, id uuid.UUID) (sqlc.CharlieConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == f.connection.ID {
		return f.connection, nil
	}
	connection, ok := f.connections[id]
	if !ok {
		return sqlc.CharlieConnection{}, pgx.ErrNoRows
	}
	return connection, nil
}
func (f *findingAccessFake) GetCharlieFinding(_ context.Context, id uuid.UUID) (sqlc.CharlieFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return sqlc.CharlieFinding{}, pgx.ErrNoRows
}
func (f *findingAccessFake) GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session, nil
}
func (f *findingAccessFake) ListCharlieFindingResources(_ context.Context, id uuid.UUID) ([]sqlc.CharlieFindingResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resources[id], nil
}
func (f *findingAccessFake) ListCharlieFindings(context.Context, sqlc.ListCharlieFindingsParams) ([]sqlc.CharlieFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	return f.rows, nil
}
func (f *findingAccessFake) GetCharlieFindingDecision(_ context.Context, requestID uuid.UUID) (sqlc.CharlieFindingDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	decision, ok := f.decisions[requestID]
	if !ok {
		return sqlc.CharlieFindingDecision{}, pgx.ErrNoRows
	}
	return decision, nil
}
func (f *findingAccessFake) TransitionCharlieFinding(_ context.Context, p sqlc.TransitionCharlieFindingParams) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transition = p
	for i := range f.rows {
		if f.rows[i].ID == p.ID && f.rows[i].Status == p.ExpectedStatus && f.rows[i].WorkflowState == p.ExpectedWorkflowState {
			f.rows[i].Status = p.NextStatus
			f.rows[i].WorkflowState = p.NextWorkflowState
			f.decisions[p.RequestID] = sqlc.CharlieFindingDecision{RequestID: p.RequestID, FindingID: p.ID, ActorRef: p.ActorRef, Decision: p.Decision, ResultStatus: p.NextStatus, ResultWorkflowState: p.NextWorkflowState}
			return p.RequestID, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}
func (f *findingAccessFake) CreateCharlieDelegation(_ context.Context, p sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	mu              sync.Mutex
	getCalls        int
	transitionCalls int
	approvalCalls   int
	dispatchCalls   int
	authRef         string
	next            string
}

func (f *findingBridgeFake) GetFinding(_ context.Context, _, auth string) (FindingAdvisoryDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	f.authRef = auth
	return FindingAdvisoryDetail{Diagnosis: "bounded diagnosis", RiskImpact: "bounded impact"}, nil
}
func (f *findingBridgeFake) TransitionFinding(_ context.Context, findingID, auth string, _ uuid.UUID, next, _ string) (BridgeFindingSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transitionCalls++
	f.authRef, f.next = auth, next
	return BridgeFindingSummary{FindingID: findingID, Status: "acknowledged"}, nil
}

func (f *findingBridgeFake) DecideApproval() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvalCalls++
}

func (f *findingBridgeFake) DispatchAction() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatchCalls++
}

type findingAuditFake struct {
	mu           sync.Mutex
	entries      []FindingLifecycleAudit
	authority    []AuthorityMutationAudit
	authorityErr error
}

func (f *findingAuditFake) RecordCharlieFindingLifecycle(_ context.Context, event FindingLifecycleAudit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, event)
}
func (f *findingAuditFake) RecordCharlieAuthorityMutation(_ context.Context, event AuthorityMutationAudit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authority = append(f.authority, event)
	return f.authorityErr
}

type findingLifecyclePublisherFake struct {
	mu     sync.Mutex
	alerts []FindingAlert
}

func (f *findingLifecyclePublisherFake) PublishCharlieFindingLifecycle(_ context.Context, alert FindingAlert) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
		connection:  connection,
		connections: map[uuid.UUID]sqlc.CharlieConnection{connection.ID: connection},
		rows:        []sqlc.CharlieFinding{{ID: findingID, ConnectionID: connection.ID, CharlieFindingID: "finding-central", SessionID: pgtype.UUID{Bytes: sessionID, Valid: true}, Severity: "warning", Status: "open", EffectiveMode: string(ModeReadOnly), WorkflowState: "manual_remediation_required", ExecutionBlockCode: "read_only", RepeatCount: 2}},
		resources:   map[uuid.UUID][]sqlc.CharlieFindingResource{findingID: {{FindingID: findingID, ResourceType: "tunnel", ResourceID: "replica-a", RequiredVerb: "read"}}},
		decisions:   make(map[uuid.UUID]sqlc.CharlieFindingDecision),
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
	if err != nil || bridge.getCalls != 1 || bridge.authRef == "" || store.delegations != 1 || view.Detail == nil {
		t.Fatalf("central detail was not live-authorized: view=%#v err=%v", view, err)
	}
	authorizer.resourcePass["replica-a"] = false
	if _, err := service.Get(context.Background(), actorID, store.rows[0].ID); err == nil || bridge.getCalls != 1 {
		t.Fatal("revoked resource authorization still reached Charlie")
	}
}

func TestFindingAccessSurvivesSignedConnectionReplacementWithoutRebindingProvenance(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	source := store.connection
	replacement := source
	replacement.ID = uuid.New()
	store.connection = replacement
	store.connections[replacement.ID] = replacement
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })

	if _, err := service.Get(context.Background(), actorID, store.rows[0].ID); err != nil {
		t.Fatalf("same-lineage replacement hid retained finding: %v", err)
	}
	requestID := uuid.New()
	if _, err := service.TransitionAdvisory(context.Background(), actorID, store.rows[0].ID, requestID, FindingAdvisoryAcknowledge); err != nil {
		t.Fatalf("same-lineage replacement blocked retained finding decision: %v", err)
	}
	if store.transition.ActorRef != findingActorRef(source.ID, actorID) || store.transition.ActorRef == findingActorRef(replacement.ID, actorID) {
		t.Fatalf("replacement changed retained finding actor reference: %q", store.transition.ActorRef)
	}
	if store.rows[0].ConnectionID != source.ID || store.session.ConnectionID != source.ID {
		t.Fatal("replacement rebound immutable finding/session provenance")
	}
}

func TestFindingAccessRejectsConnectionFromDifferentDeploymentLineage(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	replacement := store.connection
	replacement.ID = uuid.New()
	replacement.DeploymentID = "scope_other"
	store.connection = replacement
	store.connections[replacement.ID] = replacement
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })

	if _, err := service.Get(context.Background(), actorID, store.rows[0].ID); err == nil {
		t.Fatal("different deployment lineage accessed retained finding")
	}
	if bridge.getCalls != 0 || store.delegations != 0 {
		t.Fatal("cross-deployment denial reached delegation or Charlie bridge")
	}
}

func TestFindingAccessTransitionsCentralThenCommitsAndPublishes(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	view, err := service.TransitionWorkflow(context.Background(), actorID, store.rows[0].ID, uuid.New(), "start_remediation")
	if err != nil || bridge.transitionCalls != 1 || bridge.next != "start_remediation" || store.transition.ExpectedStatus != "open" ||
		store.transition.ExpectedWorkflowState != "manual_remediation_required" || store.transition.NextWorkflowState != "remediation_in_progress" ||
		view.Finding.Status != "acknowledged" {
		t.Fatalf("finding transition incomplete: view=%#v transition=%#v err=%v", view, store.transition, err)
	}
	if len(publisher.alerts) != 1 || publisher.alerts[0].Severity != "medium" || publisher.alerts[0].ResourceID != "replica-a" {
		t.Fatalf("durable lifecycle did not publish bounded alert: %#v", publisher.alerts)
	}
}

func TestFindingAccessAuditFailureCreatesNoDelegationDispatchOrTransition(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	audit.authorityErr = errors.New("database-SENTINEL")
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })

	_, err := service.TransitionWorkflow(context.Background(), actorID, store.rows[0].ID, uuid.New(), "start_remediation")
	if err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("audit failure was missing or leaked storage detail: %v", err)
	}
	if len(audit.authority) != 1 || store.delegations != 0 || bridge.transitionCalls != 0 || store.transition.ID != uuid.Nil || len(publisher.alerts) != 0 || len(audit.entries) != 0 {
		t.Fatalf("audit failure produced side effects: authority=%d delegations=%d bridge=%d transition=%#v alerts=%d lifecycle=%d",
			len(audit.authority), store.delegations, bridge.transitionCalls, store.transition, len(publisher.alerts), len(audit.entries))
	}
}

func TestFindingAccessReplaysCommittedDecisionWithoutRepublishing(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	requestID := uuid.New()
	if _, err := service.TransitionAdvisory(context.Background(), actorID, store.rows[0].ID, requestID, FindingAdvisoryAcknowledge); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionAdvisory(context.Background(), actorID, store.rows[0].ID, requestID, FindingAdvisoryAcknowledge); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if bridge.transitionCalls != 2 || len(publisher.alerts) != 1 || len(audit.entries) != 2 || audit.entries[1].OutcomeCode != "replayed" {
		t.Fatalf("replay side effects bridge=%d alerts=%d audits=%#v", bridge.transitionCalls, len(publisher.alerts), audit.entries)
	}
}

func TestFindingActorRefIsStableOpaqueAndDeploymentScoped(t *testing.T) {
	connection, actor := uuid.New(), uuid.New()
	got := findingActorRef(connection, actor)
	if got != findingActorRef(connection, actor) || got == findingActorRef(uuid.New(), actor) || strings.Contains(got, actor.String()) {
		t.Fatalf("finding actor reference is not stable and opaque: %q", got)
	}
}

func TestFindingAccessRejectsGenericLifecycleTransitionForExactPendingApproval(t *testing.T) {
	store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
	store.rows[0].ApprovalID = pgtype.Text{String: "approval-a", Valid: true}
	store.rows[0].ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
	store.rows[0].WorkflowState = "approval_pending"
	store.rows[0].ExecutionBlockCode = "approval_required"
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	if _, err := service.TransitionAdvisory(context.Background(), actorID, store.rows[0].ID, uuid.New(), FindingAdvisoryResolve); err == nil {
		t.Fatal("generic resolution bypassed the exact pending approval workflow")
	}
	if bridge.transitionCalls != 0 || store.transition.ID != uuid.Nil || len(audit.entries) != 0 || len(publisher.alerts) != 0 {
		t.Fatalf("rejected transition produced side effects: bridge=%d transition=%#v audit=%d alerts=%d",
			bridge.transitionCalls, store.transition, len(audit.entries), len(publisher.alerts))
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

func TestFindingEventAndDecisionRecheckCurrentUserResourceAuthorization(t *testing.T) {
	store, authorizer, bridge, audit, publisher, ownerID := findingAccessFixture()
	service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
	findingID := store.rows[0].ID

	if !service.CanReceiveFinding(context.Background(), ownerID, findingID) {
		t.Fatal("currently authorized owner could not receive finding event")
	}
	if service.CanReceiveFinding(context.Background(), uuid.New(), findingID) {
		t.Fatal("private finding event was visible to another resource-authorized user")
	}

	authorizer.resourcePass["replica-a"] = false
	if service.CanReceiveFinding(context.Background(), ownerID, findingID) {
		t.Fatal("revoked owner retained finding event access")
	}
	if _, err := service.Get(context.Background(), ownerID, findingID); err == nil {
		t.Fatal("revoked owner retained finding detail access")
	}
	if _, err := service.TransitionAdvisory(context.Background(), ownerID, findingID, uuid.New(), FindingAdvisoryAcknowledge); err == nil {
		t.Fatal("revoked owner retained finding decision access")
	}
	if bridge.getCalls != 0 || bridge.transitionCalls != 0 || store.transition.ID != uuid.Nil || len(publisher.alerts) != 0 {
		t.Fatalf("revoked access reached Charlie or durable workflow: bridge_get=%d bridge_transition=%d transition=%#v alerts=%d",
			bridge.getCalls, bridge.transitionCalls, store.transition, len(publisher.alerts))
	}
}

func TestFindingTransitionsRecheckLiveResourceAuthorizationWithoutExecution(t *testing.T) {
	tests := []struct {
		name     string
		decision string
		prepare  func(*findingAccessFake)
	}{
		{name: "acknowledge", decision: "acknowledge"},
		{name: "start_remediation", decision: "start_remediation"},
		{name: "request_verification", decision: "request_verification", prepare: func(store *findingAccessFake) {
			store.rows[0].Status = "acknowledged"
			store.rows[0].WorkflowState = string(FindingWorkflowRemediationInProgress)
		}},
		{name: "dismiss", decision: "dismiss"},
		{name: "resolve", decision: "resolve", prepare: func(store *findingAccessFake) {
			store.rows[0].WorkflowState = string(FindingWorkflowVerificationPending)
			store.rows[0].ExecutionBlockCode = string(ReasonVerificationFailed)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
			if test.prepare != nil {
				test.prepare(store)
			}
			authorizer.resourcePass["replica-a"] = false
			service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
			if _, err := runFindingTransition(service, actorID, store.rows[0].ID, uuid.New(), test.decision); err == nil {
				t.Fatal("revoked resource access admitted an advisory transition")
			}
			if bridge.transitionCalls != 0 || bridge.approvalCalls != 0 || bridge.dispatchCalls != 0 || store.delegations != 0 || store.transition.ID != uuid.Nil || len(publisher.alerts) != 0 {
				t.Fatalf("denied advisory produced side effects: bridge=%d approvals=%d dispatch=%d delegations=%d transition=%#v alerts=%d",
					bridge.transitionCalls, bridge.approvalCalls, bridge.dispatchCalls, store.delegations, store.transition, len(publisher.alerts))
			}
		})
	}
}

func TestFindingTransitionReplayAndConcurrencyCannotReachExecutionChannels(t *testing.T) {
	tests := []struct {
		name     string
		decision string
		prepare  func(*findingAccessFake)
	}{
		{name: "acknowledge", decision: "acknowledge"},
		{name: "start_remediation", decision: "start_remediation"},
		{name: "request_verification", decision: "request_verification", prepare: func(store *findingAccessFake) {
			store.rows[0].Status = "acknowledged"
			store.rows[0].WorkflowState = string(FindingWorkflowRemediationInProgress)
		}},
		{name: "dismiss", decision: "dismiss"},
		{name: "resolve", decision: "resolve", prepare: func(store *findingAccessFake) {
			store.rows[0].WorkflowState = string(FindingWorkflowVerificationPending)
			store.rows[0].ExecutionBlockCode = string(ReasonVerificationFailed)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, authorizer, bridge, audit, publisher, actorID := findingAccessFixture()
			if test.prepare != nil {
				test.prepare(store)
			}
			service, _ := NewFindingAccessService(store, authorizer, bridge, audit, publisher, &findingSyncerFake{}, func() bool { return true })
			requestID := uuid.New()
			findingID := store.rows[0].ID
			const callers = 12
			results := make(chan error, callers)
			var group sync.WaitGroup
			for range callers {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := runFindingTransition(service, actorID, findingID, requestID, test.decision)
					results <- err
				}()
			}
			group.Wait()
			close(results)
			succeeded := 0
			for err := range results {
				if err == nil {
					succeeded++
				}
			}
			if succeeded == 0 {
				t.Fatal("no concurrent advisory transition committed")
			}
			if _, err := runFindingTransition(service, actorID, findingID, requestID, test.decision); err != nil {
				t.Fatalf("committed advisory replay failed: %v", err)
			}
			if len(publisher.alerts) != 1 || bridge.approvalCalls != 0 || bridge.dispatchCalls != 0 {
				t.Fatalf("advisory replay crossed execution channels: alerts=%d approvals=%d dispatch=%d bridge_transitions=%d",
					len(publisher.alerts), bridge.approvalCalls, bridge.dispatchCalls, bridge.transitionCalls)
			}
		})
	}
}

func runFindingTransition(service *FindingAccessService, actorID, findingID, requestID uuid.UUID, decision string) (FindingView, error) {
	switch decision {
	case "acknowledge":
		return service.TransitionAdvisory(context.Background(), actorID, findingID, requestID, FindingAdvisoryAcknowledge)
	case "dismiss":
		return service.TransitionAdvisory(context.Background(), actorID, findingID, requestID, FindingAdvisoryDismiss)
	case "resolve":
		return service.TransitionAdvisory(context.Background(), actorID, findingID, requestID, FindingAdvisoryResolve)
	default:
		return service.TransitionWorkflow(context.Background(), actorID, findingID, requestID, decision)
	}
}
