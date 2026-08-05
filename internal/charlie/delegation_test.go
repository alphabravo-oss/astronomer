package charlie

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type delegationFake struct {
	created sqlc.CreateCharlieDelegationParams
	row     sqlc.CharlieDelegation
	getErr  error
}

func (f *delegationFake) CreateCharlieDelegation(_ context.Context, arg sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	f.created = arg
	return sqlc.CharlieDelegation{}, nil
}

func (f *delegationFake) GetActiveCharlieDelegationByHash(_ context.Context, hash string) (sqlc.CharlieDelegation, error) {
	if f.getErr != nil {
		return sqlc.CharlieDelegation{}, f.getErr
	}
	if f.row.AuthorizationHash != hash {
		return sqlc.CharlieDelegation{}, errors.New("not found")
	}
	return f.row, nil
}

func TestDelegationIsOpaqueReturnedOnceAndHashOnlyAtRest(t *testing.T) {
	fake := &delegationFake{}
	now := time.Now().UTC()
	sessionID, principalID := uuid.New(), uuid.New()
	issued, err := IssueDelegation(context.Background(), fake, sessionID, principalID, "user", 5*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if !stringsHasPrefix(issued.Reference, delegationPrefix) || issued.ExpiresAt != now.Add(5*time.Minute) {
		t.Fatalf("unexpected issued delegation: %+v", issued)
	}
	if fake.created.AuthorizationHash == "" || fake.created.AuthorizationHash == issued.Reference {
		t.Fatal("plaintext authorization reference was persisted")
	}
	if fake.created.AuthorizationHash != HashDelegation(issued.Reference) || len(fake.created.AuthorizationPrefix) > 16 {
		t.Fatal("stored delegation metadata is invalid")
	}

	fake.row = sqlc.CharlieDelegation{
		SessionID: sessionID, PrincipalID: principalID, PrincipalType: "user",
		AuthorizationHash: fake.created.AuthorizationHash,
	}
	if _, err := ValidateDelegation(context.Background(), fake, issued.Reference, DelegationExpectation{SessionID: sessionID, PrincipalID: principalID, PrincipalType: "user"}); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationRejectsUnsafeTTLAndChangedBinding(t *testing.T) {
	fake := &delegationFake{}
	if _, err := IssueDelegation(context.Background(), fake, uuid.New(), uuid.New(), "user", time.Hour, time.Now()); err == nil {
		t.Fatal("unbounded delegation TTL accepted")
	}
	if _, err := ValidateDelegation(context.Background(), fake, "wrong", DelegationExpectation{}); err == nil {
		t.Fatal("invalid authorization reference accepted")
	}

	reference := delegationPrefix + "fixture"
	fake.row = sqlc.CharlieDelegation{
		SessionID: uuid.New(), PrincipalID: uuid.New(), PrincipalType: "user",
		AuthorizationHash: HashDelegation(reference),
	}
	if _, err := ValidateDelegation(context.Background(), fake, reference, DelegationExpectation{SessionID: uuid.New(), PrincipalID: fake.row.PrincipalID, PrincipalType: "user"}); err == nil {
		t.Fatal("changed session binding accepted")
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
