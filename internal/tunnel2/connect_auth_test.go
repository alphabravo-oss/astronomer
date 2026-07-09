package tunnel2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel/connectauth"
)

// authFake implements tunnel.AgentTokenValidator for SEC-R01 authorize tests.
type authFake struct {
	regToken       string
	regClusterID   uuid.UUID
	regCreatedAt   time.Time
	durableToken   string
	durableRow     *sqlc.ClusterAgentToken
	byClusterForce *sqlc.ClusterAgentToken
	byClusterErr   error
	adoptedIDs     []uuid.UUID
}

func (f *authFake) GetRegistrationTokenByToken(_ context.Context, token string) (sqlc.ClusterRegistrationToken, error) {
	if token == "" || token != f.regToken {
		return sqlc.ClusterRegistrationToken{}, errors.New("registration token not found")
	}
	return sqlc.ClusterRegistrationToken{
		ID:        uuid.New(),
		ClusterID: f.regClusterID,
		Token:     token,
		CreatedAt: f.regCreatedAt,
	}, nil
}

func (f *authFake) MarkRegistrationTokenUsed(context.Context, uuid.UUID) error { return nil }

func (f *authFake) GetClusterAgentTokenByClusterID(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error) {
	if f.byClusterErr != nil {
		return sqlc.ClusterAgentToken{}, f.byClusterErr
	}
	if f.byClusterForce != nil {
		return *f.byClusterForce, nil
	}
	if f.durableRow != nil && f.durableRow.ClusterID == clusterID {
		return *f.durableRow, nil
	}
	return sqlc.ClusterAgentToken{}, pgx.ErrNoRows
}

func (f *authFake) GetClusterAgentTokenByToken(_ context.Context, token string) (sqlc.ClusterAgentToken, error) {
	if f.durableToken == "" || token != f.durableToken || f.durableRow == nil {
		return sqlc.ClusterAgentToken{}, errors.New("agent token not found")
	}
	return *f.durableRow, nil
}

func (f *authFake) UpsertClusterAgentToken(_ context.Context, arg sqlc.UpsertClusterAgentTokenParams) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{ID: uuid.New(), ClusterID: arg.ClusterID, TokenHash: arg.TokenHash}, nil
}

func (f *authFake) TouchClusterAgentToken(context.Context, uuid.UUID) error { return nil }

func (f *authFake) MarkClusterAgentTokenAdopted(_ context.Context, id uuid.UUID) error {
	f.adoptedIDs = append(f.adoptedIDs, id)
	return nil
}

func (f *authFake) RotateClusterAgentToken(_ context.Context, arg sqlc.RotateClusterAgentTokenParams) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{ID: arg.ID, TokenHash: arg.TokenHash}, nil
}

func (f *authFake) ClearPreviousClusterAgentTokenHash(context.Context, uuid.UUID) error { return nil }

func (f *authFake) UpdateClusterHeartbeat(context.Context, sqlc.UpdateClusterHeartbeatParams) error {
	return nil
}

func (f *authFake) UpsertClusterHealthStatus(context.Context, sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}

func (f *authFake) TouchClusterMetricsSample(context.Context, uuid.UUID) error { return nil }

func (f *authFake) CreateAgentConnection(_ context.Context, arg sqlc.CreateAgentConnectionParams) (sqlc.AgentConnection, error) {
	return sqlc.AgentConnection{ID: uuid.New(), ClusterID: arg.ClusterID}, nil
}

func (f *authFake) DisconnectActiveConnectionsByCluster(context.Context, uuid.UUID) error {
	return nil
}

func (f *authFake) UpdateAgentConnectionStatus(context.Context, sqlc.UpdateAgentConnectionStatusParams) error {
	return nil
}

func (f *authFake) UpdateAgentConnectionPing(context.Context, uuid.UUID) error { return nil }

func (f *authFake) ClaimPendingAgentLifecycleOperation(context.Context, uuid.UUID) (sqlc.AgentLifecycleOperation, error) {
	return sqlc.AgentLifecycleOperation{}, errors.New("none")
}

func (f *authFake) CompleteAgentLifecycleOperation(_ context.Context, arg sqlc.CompleteAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	return sqlc.AgentLifecycleOperation{ID: arg.ID}, nil
}

func (f *authFake) MarkRunningAgentUpgradeSucceededByVersion(context.Context, sqlc.MarkRunningAgentUpgradeSucceededByVersionParams) (int64, error) {
	return 0, nil
}

func authRequest(clusterID, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/"+clusterID+"/", nil)
	req.Header.Set(HeaderClusterID, clusterID)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestAuthorize_PostAdoptionRegistrationDenied pins SEC-R01: once a durable
// is adopted, replaying an older registration token must fail on tunnel2 the
// same way as on the hub (A3).
func TestAuthorize_PostAdoptionRegistrationDenied(t *testing.T) {
	clusterID := uuid.New()
	adoptedAt := time.Now().Add(-time.Hour)
	fake := &authFake{
		regToken:     "leaked-reg-token",
		regClusterID: clusterID,
		regCreatedAt: adoptedAt.Add(-30 * time.Minute),
		byClusterForce: &sqlc.ClusterAgentToken{
			ID:        uuid.New(),
			ClusterID: clusterID,
			TokenHash: auth.HashOpaqueToken("durable"),
			AdoptedAt: pgtype.Timestamptz{Time: adoptedAt, Valid: true},
		},
	}
	rs := NewRemoteServer(nil, fake)
	authz := rs.authorize(fake)

	_, ok, err := authz(authRequest(clusterID.String(), "leaked-reg-token"))
	if ok || err == nil {
		t.Fatalf("post-adoption registration must be denied, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(err.Error(), "already redeemed") && !strings.Contains(err.Error(), "adopted") {
		t.Fatalf("expected adoption/redeem denial message, got %v", err)
	}
}

// TestAuthorize_DurableAccepted pins SEC-R01: a hashed durable agent token
// authorizes tunnel2 connect when the cluster_id matches.
func TestAuthorize_DurableAccepted(t *testing.T) {
	clusterID := uuid.New()
	token := "durable-agent-token"
	rowID := uuid.New()
	fake := &authFake{
		durableToken: token,
		durableRow: &sqlc.ClusterAgentToken{
			ID:        rowID,
			ClusterID: clusterID,
			TokenHash: auth.HashOpaqueToken(token),
		},
	}
	rs := NewRemoteServer(nil, fake)
	authz := rs.authorize(fake)

	key, ok, err := authz(authRequest(clusterID.String(), token))
	if err != nil || !ok {
		t.Fatalf("durable must be accepted, ok=%v err=%v", ok, err)
	}
	if key != clusterID.String() {
		t.Fatalf("clientKey = %q, want cluster id", key)
	}
	if len(fake.adoptedIDs) != 1 || fake.adoptedIDs[0] != rowID {
		t.Fatalf("expected MarkClusterAgentTokenAdopted once, got %v", fake.adoptedIDs)
	}
}

// TestAuthorize_ClusterMismatchDenied covers both registration and durable
// paths when the token belongs to another cluster.
func TestAuthorize_ClusterMismatchDenied(t *testing.T) {
	want := uuid.New()
	other := uuid.New()

	// Registration token for other cluster.
	fakeReg := &authFake{
		regToken:     "reg",
		regClusterID: other,
		regCreatedAt: time.Now(),
	}
	rs := NewRemoteServer(nil, fakeReg)
	if _, ok, err := rs.authorize(fakeReg)(authRequest(want.String(), "reg")); ok || err == nil {
		t.Fatal("registration cluster mismatch must deny")
	}

	// Durable for other cluster.
	fakeDur := &authFake{
		durableToken: "dur",
		durableRow: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: other, TokenHash: auth.HashOpaqueToken("dur"),
		},
	}
	if _, ok, err := NewRemoteServer(nil, fakeDur).authorize(fakeDur)(authRequest(want.String(), "dur")); ok || err == nil {
		t.Fatal("durable cluster mismatch must deny")
	}
}

// TestAuthorize_PreAdoptionRegistrationAllowed keeps the join-window path
// open: durable exists but adopted_at is NULL → registration still works.
func TestAuthorize_PreAdoptionRegistrationAllowed(t *testing.T) {
	clusterID := uuid.New()
	fake := &authFake{
		regToken:     "reg-token",
		regClusterID: clusterID,
		regCreatedAt: time.Now().Add(-time.Minute),
		byClusterForce: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken("d"),
			// AdoptedAt zero
		},
	}
	rs := NewRemoteServer(nil, fake)
	key, ok, err := rs.authorize(fake)(authRequest(clusterID.String(), "reg-token"))
	if err != nil || !ok {
		t.Fatalf("pre-adoption registration must succeed, ok=%v err=%v", ok, err)
	}
	if key != clusterID.String() {
		t.Fatalf("clientKey = %q", key)
	}
	// Sanity: shared package labels match audit wiring.
	if connectauth.TokenKindLabel(connectauth.KindRegistration) != "registration" {
		t.Fatal("registration label drift")
	}
	if connectauth.TokenKindLabel(connectauth.KindAgent) != "durable" {
		t.Fatal("durable label drift")
	}
}
