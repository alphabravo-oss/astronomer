package asyncop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type memoryStore struct {
	mu   sync.Mutex
	rows map[string]sqlc.OperationIdempotencyKey
}

func (s *memoryStore) ReserveOperationIdempotencyKey(_ context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]sqlc.OperationIdempotencyKey{}
	}
	key := arg.Scope + "\x00" + arg.IdempotencyKey
	row, ok := s.rows[key]
	if !ok {
		row = sqlc.OperationIdempotencyKey{Scope: arg.Scope, IdempotencyKey: arg.IdempotencyKey}
		s.rows[key] = row
	}
	return row, nil
}

func (s *memoryStore) AttachOperationIdempotencyKey(_ context.Context, arg sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := arg.Scope + "\x00" + arg.IdempotencyKey
	row := s.rows[key]
	row.OperationTable = arg.OperationTable
	row.OperationID = pgtype.UUID{Bytes: arg.OperationID, Valid: true}
	row.Response = append(row.Response[:0], arg.Response...)
	s.rows[key] = row
	return row, nil
}

func TestClaimKeyReplaysExactReceiptAndRejectsDigestChange(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	actorID, targetID, projectID, operationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	request := ClaimRequest{ActorID: actorID, ProjectID: projectID, Operation: "rollout.pause", Resource: "delivery_rollout", TargetID: targetID,
		IdempotencyKey: "pause-1", RequestDigest: "sha256:first", OperationTable: "delivery_rollout_events"}
	claim, err := ClaimKey(ctx, store, request)
	if err != nil || claim.Replay {
		t.Fatalf("first claim = %#v, %v", claim, err)
	}
	receipt := NewReceipt(operationID, request.Operation, request.Resource, targetID, projectID, "/status", time.Unix(123, 0))
	if err := Attach(ctx, store, claim, receipt); err != nil {
		t.Fatal(err)
	}
	replay, err := ClaimKey(ctx, store, request)
	if err != nil || !replay.Replay || replay.Receipt != receipt {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	request.RequestDigest = "sha256:different"
	if _, err := ClaimKey(ctx, store, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("digest change error = %v", err)
	}
}

func TestValidateKeyRequiresBoundedPrintableValue(t *testing.T) {
	for _, key := range []string{"", " surrounded ", "line\nbreak", string(make([]byte, MaxIdempotencyKeyBytes+1))} {
		if err := ValidateKey(key); err == nil {
			t.Fatalf("ValidateKey(%q) unexpectedly succeeded", key)
		}
	}
	if err := ValidateKey("safe-retry-key"); err != nil {
		t.Fatal(err)
	}
}
