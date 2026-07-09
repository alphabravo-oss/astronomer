package connectauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel/connectauth"
)

type fakeLookup struct {
	reg      *sqlc.ClusterRegistrationToken
	regErr   error
	byClus   *sqlc.ClusterAgentToken
	byClusEr error
	byTok    *sqlc.ClusterAgentToken
	byTokEr  error
}

func (f *fakeLookup) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	if f.regErr != nil {
		return sqlc.ClusterRegistrationToken{}, f.regErr
	}
	if f.reg == nil {
		return sqlc.ClusterRegistrationToken{}, errors.New("not found")
	}
	return *f.reg, nil
}

func (f *fakeLookup) GetClusterAgentTokenByClusterID(context.Context, uuid.UUID) (sqlc.ClusterAgentToken, error) {
	if f.byClusEr != nil {
		return sqlc.ClusterAgentToken{}, f.byClusEr
	}
	if f.byClus == nil {
		return sqlc.ClusterAgentToken{}, pgx.ErrNoRows
	}
	return *f.byClus, nil
}

func (f *fakeLookup) GetClusterAgentTokenByToken(context.Context, string) (sqlc.ClusterAgentToken, error) {
	if f.byTokEr != nil {
		return sqlc.ClusterAgentToken{}, f.byTokEr
	}
	if f.byTok == nil {
		return sqlc.ClusterAgentToken{}, errors.New("not found")
	}
	return *f.byTok, nil
}

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestValidate_RegistrationReplayDeniedAfterAdoption(t *testing.T) {
	clusterID := uuid.New()
	adoptedAt := time.Now().Add(-time.Hour)
	v := &fakeLookup{
		reg: &sqlc.ClusterRegistrationToken{
			ID: uuid.New(), ClusterID: clusterID, CreatedAt: adoptedAt.Add(-30 * time.Minute),
		},
		byClus: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken("durable"),
			AdoptedAt: ts(adoptedAt),
		},
	}
	_, err := connectauth.Validate(context.Background(), v, clusterID, "leaked-reg")
	if err == nil {
		t.Fatal("expected post-adoption registration replay to be denied")
	}
}

func TestValidate_DurableAccepted(t *testing.T) {
	clusterID := uuid.New()
	token := "durable-plain"
	v := &fakeLookup{
		regErr: errors.New("not a reg token"),
		byTok: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken(token),
		},
	}
	res, err := connectauth.Validate(context.Background(), v, clusterID, token)
	if err != nil {
		t.Fatalf("durable must be accepted: %v", err)
	}
	if res.Kind != connectauth.KindAgent {
		t.Fatalf("kind = %q, want agent", res.Kind)
	}
}

func TestValidate_ClusterMismatchDenied(t *testing.T) {
	clusterID := uuid.New()
	other := uuid.New()
	v := &fakeLookup{
		reg: &sqlc.ClusterRegistrationToken{ID: uuid.New(), ClusterID: other, CreatedAt: time.Now()},
	}
	if _, err := connectauth.Validate(context.Background(), v, clusterID, "reg"); err == nil {
		t.Fatal("registration token for another cluster must be denied")
	}

	v2 := &fakeLookup{
		regErr: errors.New("no reg"),
		byTok:  &sqlc.ClusterAgentToken{ID: uuid.New(), ClusterID: other, TokenHash: "x"},
	}
	if _, err := connectauth.Validate(context.Background(), v2, clusterID, "tok"); err == nil {
		t.Fatal("durable token for another cluster must be denied")
	}
}

func TestValidate_JoinWindowBeforeAdoptionAllowsReg(t *testing.T) {
	clusterID := uuid.New()
	v := &fakeLookup{
		reg: &sqlc.ClusterRegistrationToken{
			ID: uuid.New(), ClusterID: clusterID, CreatedAt: time.Now().Add(-time.Minute),
		},
		byClus: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken("d"),
			// AdoptedAt zero/NULL
		},
	}
	res, err := connectauth.Validate(context.Background(), v, clusterID, "reg")
	if err != nil {
		t.Fatalf("pre-adoption reg must be allowed: %v", err)
	}
	if res.Kind != connectauth.KindRegistration {
		t.Fatalf("kind = %q, want registration", res.Kind)
	}
}
