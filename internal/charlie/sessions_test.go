package charlie

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type sessionQueriesFake struct {
	connection sqlc.CharlieConnection
	session    sqlc.CharlieSession
	lookupErr  error
	created    int
	resources  []sqlc.AddCharlieSessionResourceParams
	delegation sqlc.CreateCharlieDelegationParams
	revoked    int
	failed     int
}

func (f *sessionQueriesFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *sessionQueriesFake) GetCharlieSessionByClientID(context.Context, sqlc.GetCharlieSessionByClientIDParams) (sqlc.CharlieSession, error) {
	return f.session, f.lookupErr
}
func (f *sessionQueriesFake) CreateCharlieSession(_ context.Context, p sqlc.CreateCharlieSessionParams) (sqlc.CharlieSession, error) {
	f.created++
	f.session = sqlc.CharlieSession{ID: uuid.New(), ConnectionID: p.ConnectionID, CharlieSessionID: p.CharlieSessionID, ClientSessionID: p.ClientSessionID, OwnerUserID: p.OwnerUserID, Source: p.Source, Visibility: p.Visibility, Intent: p.Intent, ResourceScopeSummary: p.ResourceScopeSummary, State: p.State}
	return f.session, nil
}
func (f *sessionQueriesFake) AddCharlieSessionResource(_ context.Context, p sqlc.AddCharlieSessionResourceParams) error {
	f.resources = append(f.resources, p)
	return nil
}
func (f *sessionQueriesFake) CreateCharlieDelegation(_ context.Context, p sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.delegation = p
	return sqlc.CharlieDelegation{SessionID: p.SessionID}, nil
}
func (f *sessionQueriesFake) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, pgx.ErrNoRows
}
func (f *sessionQueriesFake) BindCharlieSessionCentralID(_ context.Context, p sqlc.BindCharlieSessionCentralIDParams) (sqlc.CharlieSession, error) {
	f.session.CharlieSessionID = p.CharlieSessionID
	f.session.CentralRevision = p.CentralRevision
	f.session.State = "active"
	return f.session, nil
}
func (f *sessionQueriesFake) FailCreatingCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	f.failed++
	f.session.State = "failed"
	return f.session, nil
}
func (f *sessionQueriesFake) RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error) {
	f.revoked++
	return 1, nil
}

type sessionBridgeFake struct {
	request BridgeSessionRequest
	key     string
	receipt BridgeSessionReceipt
	err     error
	calls   int
}

func (f *sessionBridgeFake) CreateSession(_ context.Context, request BridgeSessionRequest, key string) (BridgeSessionReceipt, error) {
	f.calls++
	f.request, f.key = request, key
	return f.receipt, f.err
}

type contextProviderFake struct {
	value SREContext
	err   error
}

func (f contextProviderFake) Context(context.Context, []SessionResource, string, string) (SREContext, error) {
	return f.value, f.err
}

func readySessionConnection() sqlc.CharlieConnection {
	return sqlc.CharlieConnection{
		ID: uuid.New(), InstallationID: uuid.New(), Active: true,
		OnboardingState: "active", RequestedMode: string(ModeReadOnly), VerifiedMode: string(ModeReadOnly),
	}
}

func validSessionInput() CreateSessionInput {
	return CreateSessionInput{
		ClientSessionID: uuid.New(), OwnerID: uuid.New(), ActorType: "user", ActorLabel: "Operator",
		Intent: "troubleshoot", Trigger: "manual", CurrentUIContext: "management/health",
		Resources: []SessionResource{{Type: "installation", ID: "local", RequiredVerb: "read"}},
	}
}

func TestSessionCreatePersistsAuthorizationBeforeBoundedBridgeCall(t *testing.T) {
	connection := readySessionConnection()
	queries := &sessionQueriesFake{connection: connection, lookupErr: pgx.ErrNoRows}
	bridge := &sessionBridgeFake{receipt: BridgeSessionReceipt{SessionID: "central-session", Revision: 7}}
	provider := contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: connection.InstallationID.String(), CorrelationRef: "corr-1"}}
	service, err := NewSessionService(queries, bridge, provider, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Unix(1000, 0) }
	input := validSessionInput()

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if queries.created != 1 || len(queries.resources) != 1 || queries.delegation.SessionID == uuid.Nil {
		t.Fatalf("local metadata/delegation was not persisted before bridge: %#v", queries)
	}
	if bridge.calls != 1 || bridge.key != input.ClientSessionID.String() || bridge.request.AuthorizationRef == "" {
		t.Fatalf("bridge request missing stable identity or opaque authorization: %#v", bridge)
	}
	if bridge.request.Context.ProductVersion == "" || bridge.request.Context.Resources[0] != input.Resources[0] {
		t.Fatalf("bridge request missing bounded versioned context: %#v", bridge.request.Context)
	}
	if created.Local.CharlieSessionID != "central-session" || created.Local.State != "active" || created.AuthorizationRef == "" {
		t.Fatalf("unexpected created session: %#v", created)
	}
	if created.AuthorizationRef == queries.delegation.AuthorizationHash || created.AuthorizationRef == queries.delegation.AuthorizationPrefix {
		t.Fatal("plaintext authorization reference was persisted")
	}
}

func TestSessionCreateIsInertAndRejectsOutOfScopeResources(t *testing.T) {
	connection := readySessionConnection()
	for name, tc := range map[string]struct {
		active bool
		mutate func(*CreateSessionInput)
	}{
		"runtime inactive": {false, func(*CreateSessionInput) {}},
		"downstream resource": {true, func(input *CreateSessionInput) {
			input.Resources = []SessionResource{{Type: "downstream_cluster", ID: "x", RequiredVerb: "read"}}
		}},
		"write verb": {true, func(input *CreateSessionInput) { input.Resources[0].RequiredVerb = "update" }},
		"same ID under different kinds": {true, func(input *CreateSessionInput) {
			input.Resources = []SessionResource{
				{Type: "management_component", ID: "shared-id", RequiredVerb: "read"},
				{Type: "agent_connection_record", ID: "shared-id", RequiredVerb: "read"},
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			queries := &sessionQueriesFake{connection: connection, lookupErr: pgx.ErrNoRows}
			bridge := &sessionBridgeFake{receipt: BridgeSessionReceipt{SessionID: "central", Revision: 1}}
			provider := contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: connection.InstallationID.String(), CorrelationRef: "corr"}}
			service, _ := NewSessionService(queries, bridge, provider, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return tc.active })
			input := validSessionInput()
			tc.mutate(&input)
			if _, err := service.Create(context.Background(), input); err == nil {
				t.Fatal("expected fail-closed rejection")
			}
			if bridge.calls != 0 || queries.created != 0 {
				t.Fatal("rejected session request had side effects")
			}
		})
	}
}

func TestSessionCreateReplayEnforcesOwnerWithoutBridge(t *testing.T) {
	connection := readySessionConnection()
	input := validSessionInput()
	existing := sqlc.CharlieSession{
		ID: uuid.New(), ConnectionID: connection.ID, ClientSessionID: input.ClientSessionID,
		CharlieSessionID: "central", OwnerUserID: pgtype.UUID{Bytes: input.OwnerID, Valid: true}, Source: "user", Visibility: "private", State: "active",
	}
	queries := &sessionQueriesFake{connection: connection, session: existing}
	bridge := &sessionBridgeFake{}
	provider := contextProviderFake{value: SREContext{}}
	service, _ := NewSessionService(queries, bridge, provider, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return true })

	result, err := service.Create(context.Background(), input)
	if err != nil || !result.Replayed || bridge.calls != 0 || queries.created != 0 {
		t.Fatalf("stable replay was not returned locally: result=%#v err=%v", result, err)
	}
	input.OwnerID = uuid.New()
	if _, err := service.Create(context.Background(), input); err == nil {
		t.Fatal("different owner reused a private session client ID")
	}
}

func TestSessionCreateRejectsWrongInstallationContextAndInvalidReceipt(t *testing.T) {
	connection := readySessionConnection()
	input := validSessionInput()
	for name, tc := range map[string]struct {
		provider   contextProviderFake
		receipt    BridgeSessionReceipt
		wantFailed int
	}{
		"wrong installation": {contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: uuid.NewString(), CorrelationRef: "corr"}}, BridgeSessionReceipt{SessionID: "central", Revision: 1}, 0},
		"oversized summary":  {contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: connection.InstallationID.String(), CorrelationRef: "corr", HealthSummary: string(make([]byte, 1025))}}, BridgeSessionReceipt{SessionID: "central", Revision: 1}, 0},
		"invalid receipt":    {contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: connection.InstallationID.String(), CorrelationRef: "corr"}}, BridgeSessionReceipt{}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			queries := &sessionQueriesFake{connection: connection, lookupErr: pgx.ErrNoRows}
			bridge := &sessionBridgeFake{receipt: tc.receipt}
			service, _ := NewSessionService(queries, bridge, tc.provider, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return true })
			_, err := service.Create(context.Background(), input)
			if err == nil || queries.revoked != 1 || queries.failed != tc.wantFailed {
				t.Fatalf("unsafe context/receipt not cleaned up: err=%v revoked=%d failed=%d", err, queries.revoked, queries.failed)
			}
		})
	}
}

func TestSessionCreateDoesNotWrapBridgeContent(t *testing.T) {
	connection := readySessionConnection()
	queries := &sessionQueriesFake{connection: connection, lookupErr: pgx.ErrNoRows}
	bridge := &sessionBridgeFake{err: errors.New("sensitive upstream response")}
	provider := contextProviderFake{value: SREContext{Schema: SREContextSchema, InstallationID: connection.InstallationID.String(), CorrelationRef: "corr"}}
	service, _ := NewSessionService(queries, bridge, provider, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return true })
	_, err := service.Create(context.Background(), validSessionInput())
	if err == nil || err.Error() == bridge.err.Error() {
		t.Fatal("expected a stable product-side error")
	}
}
