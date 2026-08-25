package handler

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeOperationIdempotencyStore struct {
	mu   sync.Mutex
	rows map[string]sqlc.OperationIdempotencyKey
}

func (s *fakeOperationIdempotencyStore) ReserveOperationIdempotencyKey(_ context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
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

func (s *fakeOperationIdempotencyStore) AttachOperationIdempotencyKey(_ context.Context, arg sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]sqlc.OperationIdempotencyKey{}
	}
	key := arg.Scope + "\x00" + arg.IdempotencyKey
	row := s.rows[key]
	if row.OperationID.Valid && uuid.UUID(row.OperationID.Bytes) != arg.OperationID {
		return sqlc.OperationIdempotencyKey{}, pgx.ErrNoRows
	}
	row.Scope, row.IdempotencyKey, row.OperationTable = arg.Scope, arg.IdempotencyKey, arg.OperationTable
	row.OperationID = pgtype.UUID{Bytes: arg.OperationID, Valid: true}
	row.Response = append(row.Response[:0], arg.Response...)
	s.rows[key] = row
	return row, nil
}
