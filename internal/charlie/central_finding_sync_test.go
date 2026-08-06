package charlie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type centralSyncSessionQueries struct {
	connection sqlc.CharlieConnection
	candidates []sqlc.CharlieSession
	sessions   map[uuid.UUID]sqlc.CharlieSession
	resources  map[uuid.UUID][]sqlc.CharlieSessionResource
	delegated  []uuid.UUID
}

func (f *centralSyncSessionQueries) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *centralSyncSessionQueries) ListCharlieFindingSyncCandidateSessions(context.Context, uuid.UUID) ([]sqlc.CharlieSession, error) {
	return f.candidates, nil
}
func (f *centralSyncSessionQueries) GetCharlieSession(_ context.Context, id uuid.UUID) (sqlc.CharlieSession, error) {
	row, ok := f.sessions[id]
	if !ok {
		return sqlc.CharlieSession{}, pgx.ErrNoRows
	}
	return row, nil
}
func (f *centralSyncSessionQueries) ListCharlieSessionResources(_ context.Context, id uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	return f.resources[id], nil
}
func (f *centralSyncSessionQueries) ListCharlieSessionsForOwner(context.Context, sqlc.ListCharlieSessionsForOwnerParams) ([]sqlc.CharlieSession, error) {
	return nil, nil
}
func (f *centralSyncSessionQueries) CreateCharlieDelegation(_ context.Context, input sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.delegated = append(f.delegated, input.SessionID)
	return sqlc.CharlieDelegation{SessionID: input.SessionID}, nil
}
func (f *centralSyncSessionQueries) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, pgx.ErrNoRows
}
func (f *centralSyncSessionQueries) AbortCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	return sqlc.CharlieSession{}, nil
}
func (f *centralSyncSessionQueries) RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *centralSyncSessionQueries) UpdateCharlieSessionCursor(context.Context, sqlc.UpdateCharlieSessionCursorParams) (sqlc.CharlieSession, error) {
	return sqlc.CharlieSession{}, nil
}

type centralSyncAuthorizer struct {
	use      map[uuid.UUID]bool
	resource map[uuid.UUID]map[string]bool
}

func (f centralSyncAuthorizer) CanUseCharlie(_ context.Context, actor uuid.UUID) (bool, error) {
	return f.use[actor], nil
}
func (f centralSyncAuthorizer) CanReadIncidentResources(_ context.Context, actor uuid.UUID, resources []sqlc.CharlieSessionResource) (bool, error) {
	for _, resource := range resources {
		if !f.resource[actor][resource.ResourceID] {
			return false, nil
		}
	}
	return len(resources) > 0, nil
}

type centralSyncBridge struct {
	responses [][]BridgeFindingSummary
	scopes    map[string]BridgeFindingScope
	calls     int
	err       error
}

func (f *centralSyncBridge) ListFindings(context.Context, string) ([]BridgeFindingSummary, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.responses) == 0 {
		return nil, nil
	}
	result := f.responses[0]
	f.responses = f.responses[1:]
	return result, nil
}

func (f *centralSyncBridge) GetFindingScope(_ context.Context, findingID, _ string) (BridgeFindingScope, error) {
	if f.err != nil {
		return BridgeFindingScope{}, f.err
	}
	scope, ok := f.scopes[findingID]
	if !ok {
		return BridgeFindingScope{}, errors.New("scope unavailable")
	}
	return scope, nil
}

type centralSyncStore struct {
	connectionIDs []uuid.UUID
	sessionIDs    []uuid.UUID
	summaries     []BridgeFindingSummary
	modes         []Mode
	result        DurableFinding
}

func (f *centralSyncStore) UpsertCentralFinding(_ context.Context, connectionID, sessionID uuid.UUID, summary BridgeFindingSummary, mode Mode) (DurableFinding, error) {
	f.connectionIDs = append(f.connectionIDs, connectionID)
	f.sessionIDs = append(f.sessionIDs, sessionID)
	f.summaries = append(f.summaries, summary)
	f.modes = append(f.modes, mode)
	return f.result, nil
}

func centralSyncFixture(t *testing.T) (*centralSyncSessionQueries, *SessionAccessService, uuid.UUID, uuid.UUID, sqlc.CharlieSession, sqlc.CharlieSession) {
	t.Helper()
	actorA, actorB := uuid.New(), uuid.New()
	connection := readySessionConnection()
	connection.RequestedMode, connection.VerifiedMode = "read_only", "read_only"
	sessionA := sqlc.CharlieSession{
		ID: uuid.New(), ConnectionID: connection.ID, CharlieSessionID: "central-session-a",
		OwnerUserID: pgtype.UUID{Bytes: actorA, Valid: true}, Source: "user", Visibility: "private", State: "active",
	}
	sessionB := sqlc.CharlieSession{
		ID: uuid.New(), ConnectionID: connection.ID, CharlieSessionID: "central-session-b",
		OwnerUserID: pgtype.UUID{Bytes: actorB, Valid: true}, Source: "user", Visibility: "private", State: "active",
	}
	queries := &centralSyncSessionQueries{
		connection: connection, candidates: []sqlc.CharlieSession{sessionA, sessionB},
		sessions: map[uuid.UUID]sqlc.CharlieSession{sessionA.ID: sessionA, sessionB.ID: sessionB},
		resources: map[uuid.UUID][]sqlc.CharlieSessionResource{
			sessionA.ID: {{SessionID: sessionA.ID, ResourceType: "installation", ResourceID: "deployment-a", RequiredVerb: "read"}},
			sessionB.ID: {{SessionID: sessionB.ID, ResourceType: "installation", ResourceID: "deployment-b", RequiredVerb: "read"}},
		},
	}
	authorizer := centralSyncAuthorizer{use: map[uuid.UUID]bool{actorA: true, actorB: true}, resource: map[uuid.UUID]map[string]bool{}}
	sessions, err := NewSessionAccessService(queries, authorizer, &contentBridgeFake{}, &sessionAuditFake{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return queries, sessions, actorA, actorB, sessionA, sessionB
}

func syncedSummary(id, session, severity, status, block string, updated time.Time) BridgeFindingSummary {
	workflow := "manual_remediation_required"
	switch status {
	case "acknowledged":
		workflow = "remediation_in_progress"
	case "dismissed":
		workflow = "dismissed"
	case "resolved":
		workflow = "resolved"
	}
	if block == "approval_required" {
		workflow = "approval_pending"
	}
	return BridgeFindingSummary{
		FindingID: id, SessionID: session, InvestigationID: "investigation-" + id,
		DeduplicationKey: stableFingerprint("central-fixture", id, session), RepeatCount: 1,
		Severity: severity, Status: status, WorkflowState: workflow, BlockCode: block, UpdatedAt: updated.UTC(),
	}
}

func syncedScope(id, session, block, resourceID string) BridgeFindingScope {
	return BridgeFindingScope{FindingID: id, SessionID: session, BlockCode: block,
		ResourceDigest: findingResourceDigest(resourceID), RecommendedCapability: "astronomer.argocd.self_management_sync"}
}

func TestCentralFindingSyncScopesTwoUsersSessionsAndDeployments(t *testing.T) {
	queries, sessions, actorA, actorB, sessionA, sessionB := centralSyncFixture(t)
	otherConnectionSession := sessionA
	otherConnectionSession.ID, otherConnectionSession.ConnectionID, otherConnectionSession.CharlieSessionID = uuid.New(), uuid.New(), "central-session-other-deployment"
	queries.candidates = append(queries.candidates, otherConnectionSession)
	queries.sessions[otherConnectionSession.ID] = otherConnectionSession
	queries.resources[otherConnectionSession.ID] = []sqlc.CharlieSessionResource{{SessionID: otherConnectionSession.ID, ResourceType: "installation", ResourceID: "deployment-other", RequiredVerb: "read"}}
	now := time.Unix(1000, 0)
	bridge := &centralSyncBridge{responses: [][]BridgeFindingSummary{{
		syncedSummary("finding-a", sessionA.CharlieSessionID, "medium", "open", "read_only", now),
		syncedSummary("finding-b", sessionB.CharlieSessionID, "critical", "open", "read_only", now),
		syncedSummary("finding-unknown", "removed-session", "critical", "open", "read_only", now),
	}}, scopes: map[string]BridgeFindingScope{
		"finding-a": syncedScope("finding-a", sessionA.CharlieSessionID, "read_only", "deployment-a"),
		"finding-b": syncedScope("finding-b", sessionB.CharlieSessionID, "read_only", "deployment-b"),
	}}
	store := &centralSyncStore{}
	service, _ := NewCentralFindingSyncService(queries, sessions, bridge, store, &fakeFindingPublisher{}, func() bool { return true })
	if err := service.SyncForActor(context.Background(), actorA); err != nil {
		t.Fatal(err)
	}
	if bridge.calls != 1 || len(store.summaries) != 1 || store.summaries[0].FindingID != "finding-a" || store.sessionIDs[0] != sessionA.ID || store.connectionIDs[0] != queries.connection.ID {
		t.Fatalf("cross-user/session/deployment finding leaked into sync: calls=%d summaries=%#v", bridge.calls, store.summaries)
	}
	if len(queries.delegated) != 1 || queries.delegated[0] != sessionA.ID {
		t.Fatalf("unexpected delegated sessions: %v", queries.delegated)
	}

	bridge.responses = [][]BridgeFindingSummary{{syncedSummary("finding-b", sessionB.CharlieSessionID, "critical", "open", "read_only", now)}}
	store.connectionIDs, store.sessionIDs, store.summaries, store.modes = nil, nil, nil, nil
	queries.delegated = nil
	if err := service.SyncForActor(context.Background(), actorB); err != nil {
		t.Fatal(err)
	}
	if len(store.summaries) != 1 || store.summaries[0].FindingID != "finding-b" || store.sessionIDs[0] != sessionB.ID || len(queries.delegated) != 1 || queries.delegated[0] != sessionB.ID {
		t.Fatalf("second user's exact session scope did not sync independently: summaries=%#v delegated=%v", store.summaries, queries.delegated)
	}
}

func TestCentralReadOnlyMediumFindingBecomesActionableAndDeduplicatesReplay(t *testing.T) {
	queries, sessions, actorA, _, sessionA, _ := centralSyncFixture(t)
	now := time.Unix(2000, 0)
	summary := syncedSummary("finding-read-only", sessionA.CharlieSessionID, "medium", "open", "read_only", now)
	bridge := &centralSyncBridge{responses: [][]BridgeFindingSummary{{summary, summary}}, scopes: map[string]BridgeFindingScope{
		"finding-read-only": syncedScope("finding-read-only", sessionA.CharlieSessionID, "read_only", "deployment-a"),
	}}
	store := &centralSyncStore{result: DurableFinding{ID: uuid.NewString(), Status: "open", RepeatCount: 1, Notify: true}}
	publisher := &fakeFindingPublisher{}
	service, _ := NewCentralFindingSyncService(queries, sessions, bridge, store, publisher, func() bool { return true })
	if err := service.SyncForActor(context.Background(), actorA); err != nil {
		t.Fatal(err)
	}
	if len(store.summaries) != 1 || store.modes[0] != ModeReadOnly || len(publisher.alerts) != 1 || publisher.alerts[0].Severity != "medium" || publisher.alerts[0].BlockCode != "read_only" || publisher.alerts[0].ResourceID != "deployment-a" {
		t.Fatalf("read-only finding was not actionable and deduplicated: store=%#v alerts=%#v", store.summaries, publisher.alerts)
	}
}

func TestCentralFindingSyncInactiveEmergencyAndOutageFailClosed(t *testing.T) {
	for _, test := range []struct {
		name      string
		active    bool
		emergency bool
		bridgeErr error
	}{
		{name: "inactive", active: false},
		{name: "emergency", active: true, emergency: true},
		{name: "central outage", active: true, bridgeErr: errors.New("unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			queries, sessions, actorA, _, sessionA, _ := centralSyncFixture(t)
			queries.connection.EmergencyDisabled = test.emergency
			bridge := &centralSyncBridge{err: test.bridgeErr, responses: [][]BridgeFindingSummary{{syncedSummary("finding-a", sessionA.CharlieSessionID, "high", "open", "read_only", time.Now())}}}
			store := &centralSyncStore{}
			service, _ := NewCentralFindingSyncService(queries, sessions, bridge, store, &fakeFindingPublisher{}, func() bool { return test.active })
			err := service.SyncForActor(context.Background(), actorA)
			if err == nil || len(store.summaries) != 0 {
				t.Fatalf("inactive/outage sync did not fail closed: err=%v summaries=%#v", err, store.summaries)
			}
			if (test.name == "inactive" || test.name == "emergency") && bridge.calls != 0 {
				t.Fatalf("%s reached central bridge", test.name)
			}
		})
	}
}

func TestCentralFindingNotificationEscalationCooldownAndClosedStates(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	prior := sqlc.CharlieFinding{Severity: "medium", Status: "open", ExecutionBlockCode: "read_only", UpdatedAt: now.Add(-time.Minute)}
	tests := []struct {
		name    string
		prior   sqlc.CharlieFinding
		err     error
		summary BridgeFindingSummary
		want    bool
	}{
		{name: "new medium", err: pgx.ErrNoRows, summary: syncedSummary("f", "s", "medium", "open", "read_only", now), want: true},
		{name: "same inside cooldown", prior: prior, summary: syncedSummary("f", "s", "medium", "open", "read_only", now), want: false},
		{name: "severity escalation", prior: prior, summary: syncedSummary("f", "s", "critical", "open", "read_only", now), want: true},
		{name: "cooldown elapsed", prior: sqlc.CharlieFinding{Severity: "medium", Status: "open", ExecutionBlockCode: "read_only", UpdatedAt: now.Add(-DefaultFindingAlertCooldown - time.Second)}, summary: syncedSummary("f", "s", "medium", "open", "read_only", now), want: true},
		{name: "resolved critical", prior: prior, summary: syncedSummary("f", "s", "critical", "resolved", "read_only", now), want: false},
		{name: "reopened", prior: sqlc.CharlieFinding{Severity: "medium", Status: "resolved", ExecutionBlockCode: "read_only", UpdatedAt: now.Add(-time.Minute)}, summary: syncedSummary("f", "s", "medium", "reopened", "read_only", now), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldNotifyCentralFinding(test.prior, test.err, test.summary, centralFindingStatus(test.summary.Status, test.summary.WorkflowState), now); got != test.want {
				t.Fatalf("notify=%v want %v", got, test.want)
			}
		})
	}
}

func TestCentralFindingReplayRejectsSameOrOlderCentralRevision(t *testing.T) {
	now := time.Unix(20_000, 0).UTC()
	prior := sqlc.CharlieFinding{UpdatedAt: now}
	if !centralFindingReplay(prior, syncedSummary("f", "s", "medium", "open", "read_only", now)) ||
		!centralFindingReplay(prior, syncedSummary("f", "s", "medium", "open", "read_only", now.Add(-time.Second))) ||
		centralFindingReplay(prior, syncedSummary("f", "s", "medium", "open", "read_only", now.Add(time.Second))) {
		t.Fatal("central finding replay ordering is not monotonic")
	}
}

func TestCentralFindingSummaryRejectsContentAndDisplayUsesNoCentralCanary(t *testing.T) {
	canary := "central-detail-content-canary"
	decoder := json.NewDecoder(bytes.NewBufferString(`[{"finding_id":"f","session_id":"s","investigation_id":"i","deduplication_key":"` + strings.Repeat("a", 64) + `","repeat_count":1,"severity":"high","status":"open","workflow_state":"manual_remediation_required","block_code":"read_only","updated_at":"2026-08-05T00:00:00Z","summary":"` + canary + `"}]`))
	decoder.DisallowUnknownFields()
	var summaries []BridgeFindingSummary
	if err := decoder.Decode(&summaries); err == nil {
		t.Fatal("central content field was accepted by the background summary contract")
	}
	title, summary := centralFindingDisplay("high", "read_only")
	if strings.Contains(title+summary, canary) {
		t.Fatal("central content canary reached locally persisted display metadata")
	}
}

func TestCentralFindingAcceptsExactVerificationFailureBlock(t *testing.T) {
	if !validCentralFindingBlockCode("verification_failed") {
		t.Fatal("signed post-verification failure cannot become an actionable product finding")
	}
	if validCentralFindingBlockCode("post_verification_failed_with_details") {
		t.Fatal("unbounded verification failure code was accepted")
	}
}
